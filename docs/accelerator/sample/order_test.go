package orders

import "testing"

func TestTotalCentsBaseline(t *testing.T) {
	tests := []struct {
		name  string
		lines []Line
		want  int64
	}{
		{name: "empty"},
		{name: "one unit", lines: []Line{{UnitPriceCents: 125, Quantity: 1}}, want: 125},
		{
			name:  "multiple single-unit lines",
			lines: []Line{{UnitPriceCents: 125, Quantity: 1}, {UnitPriceCents: 250, Quantity: 1}},
			want:  375,
		},
		{name: "free item", lines: []Line{{UnitPriceCents: 0, Quantity: 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TotalCents(tt.lines); got != tt.want {
				t.Fatalf("TotalCents(%v) = %d, want %d", tt.lines, got, tt.want)
			}
		})
	}
}
