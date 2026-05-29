package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/operators"
)

// TestOperatorsStoreListAndGet — the #16 operators read store projects each
// operator with its is_staff flag, TOTP-enrolled status, active/deactivated
// state, and site allowlist. List is ordered by email; Get returns one.
func TestOperatorsStoreListAndGet(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	store := operators.New(srv.Pool)

	// A TOTP-enrolled staff operator and a non-staff operator scoped to one site.
	enrolledOperator(t, ctx, srv, "admin@acme.test", true)
	clientID := insertClient(t, ctx, srv, "AcmeCorp")
	siteID := insertSite(t, ctx, srv, clientID, "Store 54")
	scopedID := insertNonStaffOperator(t, ctx, srv, "field@acme.test")
	grantSite(t, ctx, srv, scopedID, siteID)

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	// Ordered by email: admin@ before field@.
	if list[0].Email != "admin@acme.test" || list[1].Email != "field@acme.test" {
		t.Fatalf("order = %q, %q", list[0].Email, list[1].Email)
	}

	admin := list[0]
	if !admin.IsStaff || !admin.TotpEnrolled || admin.Deactivated {
		t.Errorf("admin = %+v, want staff + enrolled + active", admin)
	}
	if len(admin.SiteIDs) != 0 {
		t.Errorf("staff site allowlist = %v, want empty (full fleet implicit)", admin.SiteIDs)
	}

	field := list[1]
	if field.IsStaff || field.TotpEnrolled {
		t.Errorf("field = %+v, want non-staff + not-enrolled", field)
	}
	if len(field.SiteIDs) != 1 || field.SiteIDs[0] != siteID {
		t.Errorf("field allowlist = %v, want [%s]", field.SiteIDs, siteID)
	}

	got, err := store.Get(ctx, scopedID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != scopedID || got.Email != "field@acme.test" {
		t.Errorf("Get = %+v", got)
	}

	if _, err := store.Get(ctx, "00000000-0000-0000-0000-0000000000ff"); !errors.Is(err, operators.ErrNotFound) {
		t.Errorf("Get(unknown) err = %v, want ErrNotFound", err)
	}
	if _, err := store.Get(ctx, "not-a-uuid"); !errors.Is(err, operators.ErrNotFound) {
		t.Errorf("Get(non-uuid) err = %v, want ErrNotFound", err)
	}
}
