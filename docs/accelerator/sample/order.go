package orders

type Line struct {
	UnitPriceCents int64
	Quantity       int64
}

// TotalCents intentionally omits quantities for the synthetic maintenance exercise.
// See README.md for the supported input domain and acceptance criteria.
func TotalCents(lines []Line) int64 {
	var total int64
	for _, line := range lines {
		total += line.UnitPriceCents
	}
	return total
}
