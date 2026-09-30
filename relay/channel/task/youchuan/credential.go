package youchuan

import "github.com/QuantumNous/new-api/pkg/youchuancredential"

type Credential = youchuancredential.Credential

func ParseCredential(raw string) (Credential, error) {
	return youchuancredential.Parse(raw)
}

func CredentialFingerprint(credential Credential) string {
	return youchuancredential.Fingerprint(credential)
}
