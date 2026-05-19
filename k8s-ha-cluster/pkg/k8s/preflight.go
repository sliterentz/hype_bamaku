package k8s

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type kubeConfigFile struct {
	Clusters []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                   string `yaml:"server"`
			CertificateAuthorityData string `yaml:"certificate-authority-data"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			ClientCertificateData string `yaml:"client-certificate-data"`
			ClientKeyData         string `yaml:"client-key-data"`
		} `yaml:"user"`
	} `yaml:"users"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster string `yaml:"cluster"`
			User    string `yaml:"user"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	CurrentContext string `yaml:"current-context"`
}

func PreflightKubeConfig(kubeconfigYAML string) (string, error) {
	var cfg kubeConfigFile
	if err := yaml.Unmarshal([]byte(kubeconfigYAML), &cfg); err != nil {
		return "", fmt.Errorf("kubeconfig YAML tidak bisa diparse: %w", err)
	}
	if strings.TrimSpace(cfg.CurrentContext) == "" {
		return "", fmt.Errorf("kubeconfig tidak memiliki current-context")
	}

	ctxCluster := ""
	ctxUser := ""
	for _, c := range cfg.Contexts {
		if c.Name == cfg.CurrentContext {
			ctxCluster = c.Context.Cluster
			ctxUser = c.Context.User
			break
		}
	}
	if ctxCluster == "" || ctxUser == "" {
		return "", fmt.Errorf("current-context tidak menunjuk cluster/user yang valid: %s", cfg.CurrentContext)
	}

	server := ""
	caB64 := ""
	for _, c := range cfg.Clusters {
		if c.Name == ctxCluster {
			server = c.Cluster.Server
			caB64 = c.Cluster.CertificateAuthorityData
			break
		}
	}
	if server == "" {
		return "", fmt.Errorf("cluster server tidak ditemukan untuk context: %s", cfg.CurrentContext)
	}

	certB64 := ""
	keyB64 := ""
	for _, u := range cfg.Users {
		if u.Name == ctxUser {
			certB64 = u.User.ClientCertificateData
			keyB64 = u.User.ClientKeyData
			break
		}
	}
	if certB64 == "" || keyB64 == "" {
		return "", fmt.Errorf("client certificate/key data tidak ditemukan untuk user: %s", ctxUser)
	}

	caPEM, err := base64.StdEncoding.DecodeString(strings.TrimSpace(caB64))
	if err != nil {
		return "", fmt.Errorf("certificate-authority-data tidak valid (base64): %w", err)
	}
	clientCertPEM, err := base64.StdEncoding.DecodeString(strings.TrimSpace(certB64))
	if err != nil {
		return "", fmt.Errorf("client-certificate-data tidak valid (base64): %w", err)
	}
	clientKeyPEM, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyB64))
	if err != nil {
		return "", fmt.Errorf("client-key-data tidak valid (base64): %w", err)
	}

	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(caPEM); !ok {
		return "", fmt.Errorf("CA cert tidak bisa dimuat dari kubeconfig")
	}

	clientPair, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		return "", fmt.Errorf("client cert/key tidak valid: %w", err)
	}
	if len(clientPair.Certificate) == 0 {
		return "", fmt.Errorf("client certificate chain kosong")
	}
	leaf, err := x509.ParseCertificate(clientPair.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("client certificate tidak bisa diparse: %w", err)
	}
	now := time.Now()
	if now.After(leaf.NotAfter) {
		return "", fmt.Errorf("client certificate sudah kedaluwarsa pada %s", leaf.NotAfter.Format(time.RFC3339))
	}
	if now.Before(leaf.NotBefore) {
		return "", fmt.Errorf("client certificate belum valid sampai %s", leaf.NotBefore.Format(time.RFC3339))
	}

	u, err := url.Parse(server)
	if err != nil {
		return "", fmt.Errorf("server URL di kubeconfig tidak valid: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)

	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("konektivitas TCP ke apiserver gagal (%s): %w", addr, err)
	}
	_ = conn.Close()

	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      pool,
		Certificates: []tls.Certificate{clientPair},
	}
	if net.ParseIP(host) == nil {
		tlsCfg.ServerName = host
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}

	healthURL := strings.TrimRight(server, "/") + "/healthz"
	resp, err := client.Get(healthURL)
	if err != nil {
		return "", fmt.Errorf("apiserver tidak responsif via kubeconfig (%s): %w", healthURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("apiserver merespons non-2xx pada %s: %s", healthURL, resp.Status)
	}

	return fmt.Sprintf("ok (endpoint=%s, clientCertExpires=%s)", addr, leaf.NotAfter.Format(time.RFC3339)), nil
}
