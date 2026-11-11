import { useEffect, useRef, useState, useCallback } from 'react';
import type { WSMessage } from '../types/spec';

interface UseWebSocketOptions {
  url?: string;
  onMessage?: (msg: WSMessage) => void;
  onReconnect?: () => void;
  autoConnect?: boolean;
}

export function useWebSocket({
  url = `ws://${window.location.host}/ws`,
  onMessage,
  onReconnect,
  autoConnect = true,
}: UseWebSocketOptions = {}) {
  const [isConnected, setIsConnected] = useState(false);
  const [lastMessage, setLastMessage] = useState<WSMessage | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const reconnectTimeoutRef = useRef<number | null>(null);
  const isFirstConnectRef = useRef(true);

  const connect = useCallback(() => {
    if (socketRef.current?.readyState === WebSocket.OPEN) return;

    try {
      const ws = new WebSocket(url);
      socketRef.current = ws;

      ws.onopen = () => {
        setIsConnected(true);
        if (!isFirstConnectRef.current) {
          onReconnect?.();
        }
        isFirstConnectRef.current = false;
      };

      ws.onmessage = (event) => {
        try {
          const msg: WSMessage = JSON.parse(event.data);
          setLastMessage(msg);
          onMessage?.(msg);
        } catch (e) {
          console.error('Failed to parse WebSocket message:', e);
        }
      };

      ws.onclose = () => {
        setIsConnected(false);
        socketRef.current = null;
        // Exponential/Fixed reconnect after 3 seconds
        reconnectTimeoutRef.current = window.setTimeout(() => {
          connect();
        }, 3000);
      };

      ws.onerror = () => {
        ws.close();
      };
    } catch (e) {
      console.error('WebSocket connection error:', e);
      reconnectTimeoutRef.current = window.setTimeout(() => {
        connect();
      }, 3000);
    }
  }, [url, onMessage, onReconnect]);

  useEffect(() => {
    if (autoConnect) {
      connect();
    }
    return () => {
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
      if (socketRef.current) {
        socketRef.current.close();
      }
    };
  }, [autoConnect, connect]);

  return { isConnected, lastMessage, reconnect: connect };
}

