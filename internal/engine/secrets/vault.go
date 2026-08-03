package secrets

import "errors"

var ErrVaultNotSupported = errors.New("vault backend not yet supported")

type VaultConfig struct {
	VaultAddr          string `env:"GOERP_VAULT_ADDR"`
	VaultAuthMethod    string `env:"GOERP_VAULT_AUTH_METHOD" envDefault:"kubernetes" validate:"oneof=kubernetes approle token"`
	VaultK8sRole       string `env:"GOERP_VAULT_K8S_ROLE"`
	VaultApproleRoleId string `env:"GOERP_VAULT_APPROLE_ROLE_ID"`
	VaultToken         string `env:"GOERP_VAULT_TOKEN"`
	VaultKvMount       string `env:"GOERP_VAULT_KV_MOUNT"`
}

func newVaultBackend() (Backend, error) {
	return nil, ErrVaultNotSupported
}
