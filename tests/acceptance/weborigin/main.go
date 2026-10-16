// Command weborigin is the backend:S08 acceptance deployment front: it serves
// the production Web build at the exact HTTPS origin the control plane trusts
// and routes the API paths to the real control-plane process.
//
// It is acceptance tooling, not product behaviour: it substitutes nothing
// inside the control plane (the real cmd/server runs unchanged behind it) and
// it never holds a credential — the browser talks to this listener, this
// listener talks to 127.0.0.1.
//
// The trusted origin configuration of the identity surface requires an exact
// https origin, so this front is what makes a local host:port a valid
// deployment origin. The certificate is either supplied by the operator
// (KOWA_TLS_CERT / KOWA_TLS_KEY) or generated once and kept in KOWA_TLS_DIR,
// so its fingerprint is stable across restarts and can be trusted once.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("weborigin: %v", err)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func run() error {
	address := envOr("KOWA_WEB_ORIGIN_ADDR", "127.0.0.1:8443")
	controlPlane := envOr("KOWA_CONTROL_PLANE_URL", "http://127.0.0.1:8080")
	distDir := envOr("KOWA_WEB_DIST", "dist")
	tlsDir := envOr("KOWA_TLS_DIR", filepath.Join(os.TempDir(), "kowa-acceptance-tls"))

	target, err := url.Parse(controlPlane)
	if err != nil || target.Host == "" {
		return errors.New("KOWA_CONTROL_PLANE_URL must be an absolute url")
	}
	if info, statErr := os.Stat(distDir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("the Web build directory %s does not exist (run the production build first)", distDir)
	}
	certificate, fingerprint, certPath, err := loadCertificate(address, tlsDir)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, proxyErr error) {
		// The control plane being down is an honest 502 with no substituted
		// success; nothing here retries or falls back.
		http.Error(writer, "control plane unavailable: "+proxyErr.Error(), http.StatusBadGateway)
	}
	handler := originHandler(proxy, distDir)

	listener, err := tls.Listen("tcp", address, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	log.Printf("weborigin: https://%s -> %s (dist=%s)", address, target, distDir)
	log.Printf("weborigin: certificate %s sha256=%s", certPath, fingerprint)
	log.Printf("weborigin: trust this certificate once in the browser/keychain; it is reused on every restart")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-stop:
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		log.Printf("weborigin stopping")
		return nil
	}
}

// originHandler serves the built Web application with the SPA fallback the
// deployment contract requires and routes the API and health paths to the real
// control plane.
func originHandler(api http.Handler, distDir string) http.Handler {
	files := http.FileServer(http.Dir(distDir))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if strings.HasPrefix(path, "/api/") || path == "/healthz" || path == "/readyz" {
			api.ServeHTTP(writer, request)
			return
		}
		cleaned := filepath.Clean("/" + path)
		if info, err := os.Stat(filepath.Join(distDir, cleaned)); err == nil && !info.IsDir() {
			files.ServeHTTP(writer, request)
			return
		}
		http.ServeFile(writer, request, filepath.Join(distDir, "index.html"))
	})
}

// loadCertificate reuses the operator-supplied certificate when given, and
// otherwise generates one self-signed certificate for the origin host and
// keeps it in the run directory so its fingerprint stays stable.
func loadCertificate(address, tlsDir string) (tls.Certificate, string, string, error) {
	certPath := os.Getenv("KOWA_TLS_CERT")
	keyPath := os.Getenv("KOWA_TLS_KEY")
	if (certPath == "") != (keyPath == "") {
		return tls.Certificate{}, "", "", errors.New("KOWA_TLS_CERT and KOWA_TLS_KEY must be set together")
	}
	if certPath != "" {
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, "", "", fmt.Errorf("load the supplied certificate: %w", err)
		}
		fingerprint, err := fingerprintOf(&certificate)
		if err != nil {
			return tls.Certificate{}, "", "", err
		}
		return certificate, fingerprint, certPath, nil
	}

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return tls.Certificate{}, "", "", errors.New("KOWA_WEB_ORIGIN_ADDR must be host:port")
	}
	certPath = filepath.Join(tlsDir, "origin-cert.pem")
	keyPath = filepath.Join(tlsDir, "origin-key.pem")
	if _, err := os.Stat(certPath); err == nil {
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, "", "", fmt.Errorf("load the generated certificate: %w", err)
		}
		fingerprint, err := fingerprintOf(&certificate)
		if err != nil {
			return tls.Certificate{}, "", "", err
		}
		return certificate, fingerprint, certPath, nil
	}
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		return tls.Certificate{}, "", "", err
	}
	certificate, err := generateCertificate(host)
	if err != nil {
		return tls.Certificate{}, "", "", err
	}
	certPEM, keyPEM, err := encodeCertificate(certificate)
	if err != nil {
		return tls.Certificate{}, "", "", err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, "", "", err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, "", "", err
	}
	loaded, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", "", err
	}
	fingerprint, err := fingerprintOf(&loaded)
	if err != nil {
		return tls.Certificate{}, "", "", err
	}
	return loaded, fingerprint, certPath, nil
}

func generateCertificate(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Kowa local acceptance origin"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

func encodeCertificate(certificate *tls.Certificate) ([]byte, []byte, error) {
	key, ok := certificate.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("unexpected generated key type")
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func fingerprintOf(certificate *tls.Certificate) (string, error) {
	if len(certificate.Certificate) == 0 {
		return "", errors.New("certificate carries no leaf")
	}
	sum := sha256.Sum256(certificate.Certificate[0])
	return hex.EncodeToString(sum[:]), nil
}
