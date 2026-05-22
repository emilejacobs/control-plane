package authz

import (
	"context"
	"testing"
)

func TestScopeForStaffOperatorIsAll(t *testing.T) {
	// A staff operator's scope is resolved from the JWT claim alone — no DB.
	z := New(nil)

	f, err := z.ScopeForOperator(context.Background(), "00000000-0000-0000-0000-0000000000aa", true)
	if err != nil {
		t.Fatalf("ScopeForOperator: %v", err)
	}
	if !f.All {
		t.Errorf("staff operator: SiteFilter.All = false, want true")
	}
}
