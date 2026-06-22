package controller

import (
	"testing"

	"ai-storage-orchestrator/pkg/config"

	"github.com/stretchr/testify/assert"
)

// config 미지정(nil) 시 빌트인 기본 매핑(storage-l1..s3)으로 동작해야 한다.
// MockK8sClient 는 autoscaling_test.go 에 정의된 것을 재사용한다.
func TestTierToStorageClass_Defaults(t *testing.T) {
	pc := NewProvisioningController(new(MockK8sClient), nil, nil, nil)

	assert.Equal(t, "storage-l1", pc.tierToStorageClass("L1"))
	assert.Equal(t, "storage-l2", pc.tierToStorageClass("L2"))
	assert.Equal(t, "storage-l3", pc.tierToStorageClass("L3"))
	assert.Equal(t, "storage-s3", pc.tierToStorageClass("S3"))

	// 레거시 별칭은 normalizeTier 경유로 정규화된다.
	assert.Equal(t, "storage-l1", pc.tierToStorageClass("cache"))   // → L1
	assert.Equal(t, "storage-s3", pc.tierToStorageClass("archive")) // → S3

	// 알 수 없는 tier 는 빈 문자열(호출 측에서 default 로 보정).
	assert.Equal(t, "", pc.tierToStorageClass("bogus"))

	// 기본 fallback SC.
	assert.Equal(t, "storage-l2", pc.defaultStorageClass)
}

// config 가 일부 tier 만 override 해도 나머지는 기본값이 유지돼야 한다(생성자 병합).
func TestTierToStorageClass_ConfigOverride(t *testing.T) {
	cfg := &config.ProvisioningConfig{
		DefaultStorageClass: "storage-l3",
		TierStorageClasses: map[string]string{
			"L1": "fast-nvme",
		},
	}
	pc := NewProvisioningController(new(MockK8sClient), nil, nil, cfg)

	assert.Equal(t, "fast-nvme", pc.tierToStorageClass("L1"))  // overridden
	assert.Equal(t, "storage-l2", pc.tierToStorageClass("L2")) // default kept
	assert.Equal(t, "storage-s3", pc.tierToStorageClass("S3")) // default kept
	assert.Equal(t, "storage-l3", pc.defaultStorageClass)      // configurable fallback
}

// 소문자 tier 키로 override 해도 정규화돼 매칭돼야 한다.
func TestTierToStorageClass_LowercaseKeyOverride(t *testing.T) {
	cfg := &config.ProvisioningConfig{
		TierStorageClasses: map[string]string{
			"l2": "hot-tier",
		},
	}
	pc := NewProvisioningController(new(MockK8sClient), nil, nil, cfg)

	assert.Equal(t, "hot-tier", pc.tierToStorageClass("L2"))
	assert.Equal(t, "hot-tier", pc.tierToStorageClass("performance")) // legacy → L2
}

// tierForStorageClass 는 SC 이름에서 tier 를 역추론한다(미매칭 시 S3).
func TestTierForStorageClass(t *testing.T) {
	pc := NewProvisioningController(new(MockK8sClient), nil, nil, nil)

	assert.Equal(t, "L2", pc.tierForStorageClass("storage-l2"))
	assert.Equal(t, "S3", pc.tierForStorageClass("nonexistent-sc"))
}
