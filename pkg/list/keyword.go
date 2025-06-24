package list

type Direction int

const (
	FromFront Direction = iota
	FromBack
)

type Order int

const (
	DESC Order = iota
	ASC
)
