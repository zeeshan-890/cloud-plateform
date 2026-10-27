package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// GenerateEd25519 creates a new Ed25519 SSH keypair in OpenSSH format.
func GenerateEd25519(comment string) (publicKey string, privateKeyPEM string, err error) {
	if comment == "" {
		comment = "jp-cloud-deploy-key"
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("ssh new public key: %w", err)
	}
	pubKeyStr := fmt.Sprintf("%s %s", strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))), comment)

	privBlock, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}
	privKeyPEMStr := string(pem.EncodeToMemory(privBlock))

	return pubKeyStr, privKeyPEMStr, nil
}
