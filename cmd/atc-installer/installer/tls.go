package installer

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	corev1 "k8s.io/api/core/v1"
)

type TLS struct {
	RootCA     []byte
	ServerCert []byte
	ServerKey  []byte
}

const org = "yoke.cd"

func NewTLS(svc *corev1.Service) (*TLS, error) {
	rootTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1991),
		Subject: pkix.Name{
			Organization: []string{org},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA Key: %w", err)
	}

	rawRootCert, err := x509.CreateCertificate(rand.Reader, &rootTemplate, &rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	rootCa, err := toPem(rawRootCert, "CERTIFICATE")
	if err != nil {
		return nil, fmt.Errorf("failed to encode cerrtifcate as PEM: %w", err)
	}

	var (
		svcName      = svc.GetName()
		svcNamespace = cmp.Or(svc.GetNamespace(), "default")
	)

	serverTemplate := x509.Certificate{
		DNSNames: []string{
			svcName,
			fmt.Sprintf("%s.%s", svcName, svcNamespace),
			fmt.Sprintf("%s.%s.svc", svcName, svcNamespace),
		},
		SerialNumber: big.NewInt(1024),
		Subject: pkix.Name{
			Organization: []string{org},
			CommonName:   fmt.Sprintf("%s.%s.svc", svcName, svcNamespace),
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(5, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 5},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	rawServerCert, err := x509.CreateCertificate(rand.Reader, &serverTemplate, &rootTemplate, &serverKey.PublicKey, rootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create server certigicate: %w", err)
	}

	serverCert, err := toPem(rawServerCert, "CERTIFICATE")
	if err != nil {
		return nil, fmt.Errorf("failed to encode server certificate as PEM: %w", err)
	}

	serverKeyPEM, err := toPem(x509.MarshalPKCS1PrivateKey(serverKey), "RSA PRIVATE KEY")
	if err != nil {
		return nil, fmt.Errorf("failed to encode server key to PEM: %w", err)
	}

	return &TLS{
		RootCA:     rootCa,
		ServerCert: serverCert,
		ServerKey:  serverKeyPEM,
	}, nil
}

func toPem(data []byte, typ string) ([]byte, error) {
	var buffer bytes.Buffer
	err := pem.Encode(&buffer, &pem.Block{
		Type:  typ,
		Bytes: data,
	})
	return buffer.Bytes(), err
}
