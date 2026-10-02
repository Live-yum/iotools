package engine

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// GenerateOPCUAClientIdentity is an explicit local provisioning action, never
// called automatically while discovering or connecting. Existing files survive.
func GenerateOPCUAClientIdentity(certPath, keyPath, applicationURI string) error {
	return GenerateOPCUAClientIdentityContext(context.Background(), certPath, keyPath, applicationURI)
}
func GenerateOPCUAClientIdentityContext(ctx context.Context, certPath, keyPath, applicationURI string) error {
	random := identityRandom{ctx}
	if certPath == "" || keyPath == "" {
		return fmt.Errorf("请指定证书和私钥的新文件路径")
	}
	certAbs, e := filepath.Abs(certPath)
	if e != nil {
		return e
	}
	keyAbs, e := filepath.Abs(keyPath)
	if e != nil {
		return e
	}
	if certAbs == keyAbs {
		return fmt.Errorf("证书和私钥不能使用同一文件")
	}
	uri, e := url.Parse(applicationURI)
	if e != nil || !uri.IsAbs() || uri.Fragment != "" {
		return fmt.Errorf("Application URI必须是绝对URI且不含fragment")
	}
	for _, path := range []string{certAbs, keyAbs} {
		if _, e := os.Lstat(path); e == nil {
			return fmt.Errorf("文件已存在，拒绝覆盖：%s", path)
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	key, e := rsa.GenerateKey(random, 3072)
	if e != nil {
		return e
	}
	serial, e := rand.Int(random, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return e
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "iotools OPC UA client"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true, URIs: []*url.URL{uri}}
	der, e := x509.CreateCertificate(random, template, template, &key.PublicKey, key)
	if e != nil {
		return e
	}
	write := func(path string, data []byte) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(data)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			os.Remove(path)
		}
		return e
	}
	if e = write(certAbs, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); e != nil {
		return e
	}
	if e = write(keyAbs, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})); e != nil {
		os.Remove(certAbs)
		return e
	}
	return nil
}

type identityRandom struct{ ctx context.Context }

func (r identityRandom) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return rand.Reader.Read(b)
}
