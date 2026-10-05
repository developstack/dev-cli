package credstore

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// keychainService 是钥匙串条目的服务名；账户名是平台站点根（一个平台一条）。
const keychainService = "aidevstack-cli"

// probeAccount 只用来探测钥匙串是否可用（读一个不存在的条目：可用时返回 ErrNotFound）。
const probeAccount = "__dev-cli-probe__"

// KeychainStore 用 OS 钥匙串保存凭据。
type KeychainStore struct{}

// NewKeychainStore 创建钥匙串存储。
func NewKeychainStore() *KeychainStore { return &KeychainStore{} }

// Kind 实现 Store。
func (*KeychainStore) Kind() string { return "keychain" }

// Probe 探测钥匙串是否可用（Linux 没有 Secret Service / 无 D-Bus 会话时报错）。
func (*KeychainStore) Probe() error {
	_, err := keyring.Get(keychainService, probeAccount)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Load 实现 Store。
func (*KeychainStore) Load(platform string) (Credential, error) {
	data, err := keyring.Get(keychainService, platform)
	if errors.Is(err, keyring.ErrNotFound) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("credstore: read keychain: %w", err)
	}
	return unmarshal(platform, data)
}

// Save 实现 Store。
func (*KeychainStore) Save(cred Credential) error {
	data, err := marshal(cred)
	if err != nil {
		return err
	}
	if err := keyring.Set(keychainService, cred.Platform, data); err != nil {
		return fmt.Errorf("credstore: write keychain: %w", err)
	}
	return nil
}

// Delete 实现 Store（不存在不报错）。
func (*KeychainStore) Delete(platform string) error {
	err := keyring.Delete(keychainService, platform)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("credstore: delete keychain item: %w", err)
	}
	return nil
}
