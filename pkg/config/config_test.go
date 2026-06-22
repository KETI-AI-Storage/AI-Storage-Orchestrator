package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 파일이 없으면 에러가 아니라 빌트인 기본값을 돌려줘야 한다(ConfigMap optional).
func TestLoad_DefaultsWhenMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	assert.NoError(t, err)
	assert.Equal(t, "storage-l2", cfg.Provisioning.DefaultStorageClass)
	assert.Equal(t, "storage-l1", cfg.Provisioning.TierStorageClasses["L1"])
	assert.Equal(t, "storage-s3", cfg.Provisioning.TierStorageClasses["S3"])
}

// 일부 키만 정의해도 그 키만 덮어쓰고 나머지는 기본값을 유지해야 한다(부분 병합).
func TestLoad_PartialOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "provisioning:\n" +
		"  default_storage_class: storage-l3\n" +
		"  tier_storage_classes:\n" +
		"    L1: custom-fast\n"
	assert.NoError(t, os.WriteFile(p, []byte(yaml), 0o644))

	cfg, err := Load(p)
	assert.NoError(t, err)
	assert.Equal(t, "storage-l3", cfg.Provisioning.DefaultStorageClass)       // overridden
	assert.Equal(t, "custom-fast", cfg.Provisioning.TierStorageClasses["L1"]) // overridden
	assert.Equal(t, "storage-l2", cfg.Provisioning.TierStorageClasses["L2"])  // default kept
	assert.Equal(t, "storage-s3", cfg.Provisioning.TierStorageClasses["S3"])  // default kept
}

// 소문자 tier 키도 대문자로 정규화돼 저장돼야 한다(normalizeTier 결과와 매칭).
func TestLoad_TierKeyNormalized(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "provisioning:\n" +
		"  tier_storage_classes:\n" +
		"    l1: lower-fast\n"
	assert.NoError(t, os.WriteFile(p, []byte(yaml), 0o644))

	cfg, err := Load(p)
	assert.NoError(t, err)
	assert.Equal(t, "lower-fast", cfg.Provisioning.TierStorageClasses["L1"])
}

// 다른 섹션(migration/metrics/logging)이 함께 있어도 무시되고 에러 없이 파싱돼야 한다.
func TestLoad_IgnoresUnknownSections(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "migration:\n" +
		"  default_timeout: 600\n" +
		"logging:\n" +
		"  level: info\n" +
		"provisioning:\n" +
		"  default_storage_class: storage-l1\n"
	assert.NoError(t, os.WriteFile(p, []byte(yaml), 0o644))

	cfg, err := Load(p)
	assert.NoError(t, err)
	assert.Equal(t, "storage-l1", cfg.Provisioning.DefaultStorageClass)
}

func TestResolvePath_EnvOverride(t *testing.T) {
	t.Setenv(envConfigPath, "/custom/path/config.yaml")
	assert.Equal(t, "/custom/path/config.yaml", ResolvePath())
}

func TestResolvePath_Default(t *testing.T) {
	t.Setenv(envConfigPath, "")
	assert.Equal(t, defaultConfigPath, ResolvePath())
}
