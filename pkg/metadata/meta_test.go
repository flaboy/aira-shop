package metadata

import (
	"strings"
	"testing"

	airaWebConfig "github.com/flaboy/aira-web/pkg/config"
)

func TestMetaValueExistsConditionUsesConfiguredMetaTable(t *testing.T) {
	previousConfig := airaWebConfig.Config
	airaWebConfig.Config = &airaWebConfig.FrameworkConfig{AiraTablePreifix: "ar_"}
	defer func() {
		airaWebConfig.Config = previousConfig
	}()

	got := metaValueExistsCondition("customers")

	if !strings.Contains(got, "FROM ar_meta_values") {
		t.Fatalf("expected query to use configured meta value table, got %q", got)
	}
	if !strings.Contains(got, "ar_meta_values.target_id = customers.id") {
		t.Fatalf("expected query to reference configured meta value table, got %q", got)
	}
	if strings.Contains(got, "FROM meta_values") {
		t.Fatalf("query must not use unprefixed meta value table, got %q", got)
	}
}
