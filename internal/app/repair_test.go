package app

import (
	"testing"
)

// TestRepairStateIsReported: the start marks the rebuild before the server accepts its first
// request, reports the phase it is in, and clears it when the vault accepts writes again. The API's
// read-only window is built on exactly these three answers.
func TestRepairStateIsReported(t *testing.T) {
	a := &App{}
	if a.Repairing() {
		t.Fatal("a fresh app reports a rebuild")
	}
	if a.repairStatus() != nil {
		t.Fatal("a fresh app reports a repair status")
	}

	a.beginRepair()
	if !a.Repairing() {
		t.Fatal("beginRepair did not mark the rebuild")
	}
	if st := a.repairStatus(); st == nil || !st.Active || st.Phase != "sources" {
		t.Fatalf("repair status = %+v, want an active sources phase", st)
	}

	a.RepairProgress("claims", 120, 238_113)
	st := a.repairStatus()
	if st == nil || st.Phase != "claims" || st.Done != 120 || st.Total != 238_113 {
		t.Fatalf("repair status = %+v, want claims 120/238113", st)
	}

	a.endRepair()
	if a.Repairing() || a.repairStatus() != nil {
		t.Fatal("endRepair left the rebuild active")
	}
}
