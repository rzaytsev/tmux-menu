package agenthud

import "testing"

func TestLayoutAdaptsFromGridToSingleTerminal(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		columns, rows int
		capacity      int
	}{
		{name: "wide", width: 120, height: 30, columns: 2, rows: 2, capacity: 4},
		{name: "medium", width: 90, height: 12, columns: 2, rows: 1, capacity: 2},
		{name: "narrow", width: 52, height: 22, columns: 1, rows: 2, capacity: 2},
		{name: "minimum", width: 30, height: 8, columns: 1, rows: 1, capacity: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeLayout(tt.width, tt.height, false, false)
			if got.Columns != tt.columns || got.Rows != tt.rows || got.Capacity() != tt.capacity {
				t.Fatalf("layout = %#v, want %dx%d capacity %d", got, tt.columns, tt.rows, tt.capacity)
			}
			for _, cell := range got.Cells {
				if cell.X < 0 || cell.Y < 0 || cell.Width < 1 || cell.Height < 1 || cell.X+cell.Width > tt.width || cell.Y+cell.Height > tt.height {
					t.Fatalf("cell out of bounds: %#v in %dx%d", cell, tt.width, tt.height)
				}
			}
		})
	}
}

func TestFocusedAndHelpLayoutsReserveEssentialControls(t *testing.T) {
	focused := ComputeLayout(120, 30, true, false)
	if focused.Capacity() != 1 || focused.Columns != 1 || focused.Rows != 1 {
		t.Fatalf("focused layout = %#v", focused)
	}
	withHelp := ComputeLayout(52, 22, false, true)
	withoutHelp := ComputeLayout(52, 22, false, false)
	if withHelp.FooterRows <= withoutHelp.FooterRows || withHelp.BodyHeight >= withoutHelp.BodyHeight {
		t.Fatalf("help did not reserve footer rows: with=%#v without=%#v", withHelp, withoutHelp)
	}
}
