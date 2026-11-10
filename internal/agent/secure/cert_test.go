package secure

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCertificateAuthority_mTLSWorkflow(t *testing.T) {
	ca, err := NewCertificateAuthority()
	if err != nil {
		t.Fatalf("failed to create CA: %v", err)
	}

	// 1. Generate Server Certificate
	serverCert, _, _, err := ca.GenerateServerCert([]string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("failed to generate server cert: %v", err)
	}

	// 2. Setup mTLS Server
	serverTLSConf := ca.ServerTLSConfig(serverCert)
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID, err := ExtractClientIDFromCert(r.TLS)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("hello " + clientID))
	}))
	ts.TLS = serverTLSConf
	ts.StartTLS()
	defer ts.Close()

	// 3. Test: Client WITHOUT certificate MUST be rejected
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(ca.CAPEM())

	noCertClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:            caPool,
				InsecureSkipVerify: false,
			},
		},
	}
	_, err = noCertClient.Get(ts.URL)
	if err == nil {
		t.Fatalf("expected request without client certificate to fail with mTLS required")
	}

	// 4. Test: Client WITH valid certificate bound to client_id MUST succeed
	clientID := "cli_agent_12345678"
	clientCert, _, _, err := ca.GenerateClientCert(clientID)
	if err != nil {
		t.Fatalf("failed to generate client cert: %v", err)
	}

	clientTLSConf := ca.ClientTLSConfig(clientCert)
	mtlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: clientTLSConf,
		},
	}

	resp, err := mtlsClient.Get(ts.URL)
	if err != nil {
		t.Fatalf("mTLS request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello "+clientID {
		t.Errorf("expected response 'hello %s', got '%s'", clientID, string(body))
	}
}

func TestCertificateAuthority_EnrollTokenSingleUse(t *testing.T) {
	ca, err := NewCertificateAuthority()
	if err != nil {
		t.Fatalf("failed to create CA: %v", err)
	}

	token := ca.CreateEnrollToken(5 * time.Minute)

	// First consume MUST succeed
	if !ca.ConsumeEnrollToken(token) {
		t.Errorf("first consume of valid enroll token failed")
	}

	// Second consume MUST fail (single-use)
	if ca.ConsumeEnrollToken(token) {
		t.Errorf("second consume of same enroll token should be rejected")
	}

	// Invalid token MUST fail
	if ca.ConsumeEnrollToken("non_existent_token") {
		t.Errorf("consuming fake token should fail")
	}
}

func TestCertificateAuthority_NeedsRotation(t *testing.T) {
	now := time.Now()

	// Cert expiring in 40 days -> No rotation needed
	cert40Days := &x509.Certificate{
		NotAfter: now.Add(40 * 24 * time.Hour),
	}
	if NeedsRotation(cert40Days) {
		t.Errorf("cert expiring in 40 days should not need rotation")
	}

	// Cert expiring in 25 days -> Rotation needed (SPEC §3.3: 30 days prior)
	cert25Days := &x509.Certificate{
		NotAfter: now.Add(25 * 24 * time.Hour),
	}
	if !NeedsRotation(cert25Days) {
		t.Errorf("cert expiring in 25 days should need rotation")
	}

	// Nil cert -> needs rotation
	if !NeedsRotation(nil) {
		t.Errorf("nil cert should need rotation")
	}
}
