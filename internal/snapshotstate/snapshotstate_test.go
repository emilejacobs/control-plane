package snapshotstate_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/snapshotstate"
)

func TestLoadMissingFileIsZero(t *testing.T) {
	s := snapshotstate.NewStore(filepath.Join(t.TempDir(), "snapshot-state.json"))
	st, err := s.Load()
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if st.Cadence != "" || len(st.NextFire) != 0 {
		t.Errorf("missing-file state = %+v, want zero", st)
	}
}

func TestSetCadenceRoundTrip(t *testing.T) {
	s := snapshotstate.NewStore(filepath.Join(t.TempDir(), "snapshot-state.json"))
	if err := s.SetCadence("weekly"); err != nil {
		t.Fatalf("SetCadence: %v", err)
	}
	st, _ := s.Load()
	if st.Cadence != "weekly" {
		t.Errorf("cadence = %q, want weekly", st.Cadence)
	}
}

// SetCadence preserves the per-camera schedule; SetNextFire preserves the
// cadence — both round-trip independently (an agent restart loses neither).
func TestSetCadencePreservesSchedule(t *testing.T) {
	s := snapshotstate.NewStore(filepath.Join(t.TempDir(), "snapshot-state.json"))
	fire := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	if err := s.SetNextFire("cam1", fire); err != nil {
		t.Fatalf("SetNextFire: %v", err)
	}
	if err := s.SetCadence("daily"); err != nil {
		t.Fatalf("SetCadence: %v", err)
	}

	st, _ := s.Load()
	if st.Cadence != "daily" {
		t.Errorf("cadence = %q, want daily", st.Cadence)
	}
	if got := st.NextFire["cam1"]; !got.Equal(fire) {
		t.Errorf("next_fire[cam1] = %v, want %v (clobbered by SetCadence?)", got, fire)
	}
}
