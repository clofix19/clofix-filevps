package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/clofix/filevps/worker/pkg/proto"
	"golang.org/x/crypto/ssh"
)

func TestBuildSSHClientConfigRejectsPasswordOnKeyOnly(t *testing.T) {
	_, err := buildSSHClientConfig(terminalAuthMsg{Method: "password", Password: "CloFixRoot1"}, proto.InstanceSpec{
		SSHAuthorizedKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICegIp5reaeI63T+PqfM/IwDoWVBup+i1eIVf8lnv59f clofix",
	})
	if err == nil || !strings.Contains(err.Error(), "PEM/publickey-only") {
		t.Fatalf("expected key-only rejection, got %v", err)
	}
}

func TestBuildSSHClientConfigRejectsPublicKeyPaste(t *testing.T) {
	_, err := buildSSHClientConfig(terminalAuthMsg{
		Method:     "pem",
		PrivateKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICegIp5reaeI63T+PqfM/IwDoWVBup+i1eIVf8lnv59f clofix",
	}, proto.InstanceSpec{SSHAuthorizedKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICegIp5reaeI63T+PqfM/IwDoWVBup+i1eIVf8lnv59f clofix"})
	if err == nil || !strings.Contains(err.Error(), "private PEM") {
		t.Fatalf("expected public-key paste rejection, got %v", err)
	}
}

func TestBuildSSHClientConfigRejectsMismatchedPEM(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := string(pem.EncodeToMemory(block))
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(otherPub)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	_, err = buildSSHClientConfig(terminalAuthMsg{Method: "pem", PrivateKey: pemBytes}, proto.InstanceSpec{
		SSHAuthorizedKey: authorized,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatch rejection, got %v", err)
	}
}

func TestMapSSHDialErrorPasswordOnKeyOnly(t *testing.T) {
	msg := mapSSHDialError(errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none], no supported methods remain"), terminalAuthMsg{Method: "password"})
	if !strings.Contains(msg, "PEM key") {
		t.Fatalf("unexpected mapping: %s", msg)
	}
}
