package agenthud

const (
	hudHeaderRows   = 1
	hudCompactRows  = 1
	hudExpandedRows = 3
	hudGap          = 1
	hudWideWidth    = 72
	hudTwoRowHeight = 13
)

type Size struct {
	Width  int
	Height int
}

type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

type Layout struct {
	Width      int
	Height     int
	HeaderRows int
	FooterRows int
	BodyHeight int
	Columns    int
	Rows       int
	Cells      []Rect
}

func ComputeLayout(width, height int, focused, fullHelp bool) Layout {
	width = max(width, 1)
	height = max(height, 1)
	footerRows := hudCompactRows
	if fullHelp {
		footerRows = hudExpandedRows
	}
	footerRows = min(footerRows, max(height-hudHeaderRows, 0))
	bodyHeight := max(height-hudHeaderRows-footerRows, 0)
	columns, rows := 1, 1
	if !focused {
		if width >= hudWideWidth {
			columns = 2
		}
		if bodyHeight >= hudTwoRowHeight {
			rows = 2
		}
	}
	if bodyHeight == 0 {
		rows = 0
	}

	layout := Layout{
		Width:      width,
		Height:     height,
		HeaderRows: min(hudHeaderRows, height),
		FooterRows: footerRows,
		BodyHeight: bodyHeight,
		Columns:    columns,
		Rows:       rows,
	}
	if rows == 0 {
		return layout
	}
	cellWidth := max((width-hudGap*(columns-1))/columns, 1)
	cellHeight := max((bodyHeight-hudGap*(rows-1))/rows, 1)
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			x := column * (cellWidth + hudGap)
			y := layout.HeaderRows + row*(cellHeight+hudGap)
			cell := Rect{X: x, Y: y, Width: cellWidth, Height: cellHeight}
			if column == columns-1 {
				cell.Width = max(width-x, 1)
			}
			if row == rows-1 {
				cell.Height = max(layout.HeaderRows+bodyHeight-y, 1)
			}
			layout.Cells = append(layout.Cells, cell)
		}
	}
	return layout
}

func (l Layout) Capacity() int {
	return len(l.Cells)
}
