package config

import (
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

// envConfigPath 는 config.yaml 경로를 덮어쓰는 환경변수 이름이다.
const envConfigPath = "ORCHESTRATOR_CONFIG"

// defaultConfigPath 는 ConfigMap 이 마운트되는 표준 경로다.
// WHY: deployments/cluster-orchestrator.yaml 가 ConfigMap 을 /etc/orchestrator 에
//
//	readOnly 로 마운트한다. 환경변수 미지정 시 이 경로를 사용한다.
const defaultConfigPath = "/etc/orchestrator/config.yaml"

// Config 는 config.yaml 의 루트 구조다.
//
// WHY: migration/metrics/logging 섹션은 아직 코드에서 읽지 않으므로 의도적으로 생략한다.
//
//	sigs.k8s.io/yaml 은 매핑되지 않은 키를 무시하므로 다른 섹션이 함께 있어도 안전하다.
type Config struct {
	Provisioning ProvisioningConfig `json:"provisioning"`
}

// ProvisioningConfig 는 정책 tier(L1/L2/L3/S3) → 실제 K8s StorageClass 이름 매핑을 담는다.
//
// WHY: 기존엔 tier→SC 매핑이 Go 소스에 하드코딩(storage-l1..s3)돼 있어 SC 명명이 바뀌면
//
//	재빌드가 필요했다. 이 매핑을 ConfigMap 으로 빼면 ConfigMap 만 수정하고 재시작하면 된다.
type ProvisioningConfig struct {
	// DefaultStorageClass 는 알 수 없는 tier 가 들어왔을 때의 보정용 SC 다.
	DefaultStorageClass string `json:"default_storage_class"`
	// TierStorageClasses 는 정규화된 tier(L1/L2/L3/S3) → SC 이름 매핑이다.
	TierStorageClasses map[string]string `json:"tier_storage_classes"`
}

// DefaultProvisioningConfig 는 ConfigMap 부재(optional:true) 시 사용할 빌트인 기본값이다.
//
// WHY: 데모 클러스터 SC 4종(storage-l1/l2/l3/s3)에 맞춘 안전 기본값. 이 덕분에 ConfigMap 이
//
//	없어도 기존 동작과 동일하다. default 는 항상 존재하는 storage-l2(nvme-hot, default SC)로 둔다.
func DefaultProvisioningConfig() ProvisioningConfig {
	return ProvisioningConfig{
		DefaultStorageClass: "storage-l2",
		TierStorageClasses: map[string]string{
			"L1": "storage-l1",
			"L2": "storage-l2",
			"L3": "storage-l3",
			"S3": "storage-s3",
		},
	}
}

// ResolvePath 는 환경변수(ORCHESTRATOR_CONFIG) 우선, 없으면 기본 마운트 경로를 반환한다.
func ResolvePath() string {
	if p := strings.TrimSpace(os.Getenv(envConfigPath)); p != "" {
		return p
	}
	return defaultConfigPath
}

// Load 는 주어진 경로의 YAML 을 읽어 빌트인 기본값 위에 부분 병합(override)한다.
//
// WHY: ConfigMap 은 optional 이므로 파일이 없으면 에러가 아니라 "기본값 사용"이 정답이다.
//
//	또한 파일이 일부 키만 정의해도 그 키만 덮어쓰고 나머지는 기본값을 유지해, 운영자가
//	매핑 일부만 바꿔도 안전하게 동작한다.
func Load(path string) (*Config, error) {
	cfg := &Config{Provisioning: DefaultProvisioningConfig()}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var fileCfg Config
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if strings.TrimSpace(fileCfg.Provisioning.DefaultStorageClass) != "" {
		cfg.Provisioning.DefaultStorageClass = fileCfg.Provisioning.DefaultStorageClass
	}
	for tier, sc := range fileCfg.Provisioning.TierStorageClasses {
		if strings.TrimSpace(tier) == "" || strings.TrimSpace(sc) == "" {
			continue
		}
		// tier 키는 항상 대문자(L1/L2/L3/S3)로 정규화해 normalizeTier 결과와 매칭되게 한다.
		cfg.Provisioning.TierStorageClasses[strings.ToUpper(strings.TrimSpace(tier))] = sc
	}
	return cfg, nil
}

// LoadDefault 는 ResolvePath() 가 정한 경로에서 설정을 로드한다.
func LoadDefault() (*Config, error) {
	return Load(ResolvePath())
}
