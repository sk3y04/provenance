package downloader

import (
	"context"
	"net"
	"net/http"
	"time"

	utls "github.com/refraction-networking/utls"
)

func NewChromeTLSRoundTripper() http.RoundTripper {
	dialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				_ = conn.Close()
				return nil, err
			}

			utlsCfg := &utls.Config{
				ServerName: host,
				NextProtos: []string{"h2", "http/1.1"},
			}

			tlsConn := utls.UClient(conn, utlsCfg, utls.HelloChrome_131)
			if err := tlsConn.Handshake(); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}
