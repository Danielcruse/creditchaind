package core

func BorrowLimit(
	score uint64,
) uint64 {

	switch {
	case score >= 1000:
		return 100000

	case score >= 800:
		return 50000

	case score >= 700:
		return 25000

	case score >= 600:
		return 10000

	default:
		return 1000
	}
}