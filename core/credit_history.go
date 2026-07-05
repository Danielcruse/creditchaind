package core

type CreditEvent struct {
	Type   string
	Amount uint64
}

type CreditHistory struct {
	Events map[string][]CreditEvent
}

func NewCreditHistory() *CreditHistory {
	return &CreditHistory{
		Events: make(
			map[string][]CreditEvent,
		),
	}
}

func (c *CreditHistory) Add(
	address string,
	event CreditEvent,
) {
	c.Events[address] =
		append(
			c.Events[address],
			event,
		)
}

func (c *CreditHistory) Get(
	address string,
) []CreditEvent {
	return c.Events[address]
}