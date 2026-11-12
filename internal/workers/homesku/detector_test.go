package homesku

import (
	"testing"
)

func TestDetector_Constants(t *testing.T) {
	d := NewDetector()
	if d == nil {
		t.Fatalf("NewDetector returned nil")
	}

	// The normalized edition constants must stay stable: they are the values
	// capability detection and the local panel compare against.
	for _, edition := range []Edition{EditionHome, EditionPro, EditionEnterprise, EditionEducation, EditionUnknown} {
		if edition == "" {
			t.Fatalf("edition constant must not be empty")
		}
	}
}

func TestDetectEditionFromString(t *testing.T) {
	tests := []struct {
		name      string
		editionID string
		want      Edition
	}{
		{name: "core", editionID: "Core", want: EditionHome},
		{name: "core_n", editionID: "CoreN", want: EditionHome},
		{name: "core_single_language", editionID: "CoreSingleLanguage", want: EditionHome},
		{name: "home", editionID: "Home", want: EditionHome},
		{name: "professional", editionID: "Professional", want: EditionPro},
		{name: "professional_n", editionID: "ProfessionalN", want: EditionPro},
		{name: "enterprise", editionID: "Enterprise", want: EditionEnterprise},
		{name: "enterprise_n", editionID: "EnterpriseN", want: EditionEnterprise},
		{name: "education", editionID: "Education", want: EditionEducation},
		{name: "education_n", editionID: "EducationN", want: EditionEducation},
		{name: "server", editionID: "ServerStandard", want: EditionUnknown},
		{name: "empty", editionID: "", want: EditionUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := DetectEditionFromString(test.editionID); got != test.want {
				t.Fatalf("DetectEditionFromString(%q) = %q, want %q", test.editionID, got, test.want)
			}
		})
	}
}

func TestDetectEditionFromStringMatchesNormalizedEditions(t *testing.T) {
	// The map must produce exactly the package's normalized Edition constants.
	for _, editionID := range []string{"Professional", "Enterprise", "Education", "Core"} {
		switch got := DetectEditionFromString(editionID); got {
		case EditionHome, EditionPro, EditionEnterprise, EditionEducation:
		default:
			t.Fatalf("DetectEditionFromString(%q) = %q, not a normalized edition", editionID, got)
		}
	}
}
