package core

type Proposal struct {
	ID          string
	Title       string
	Description string
	CreatedBy   string

	YesVotes uint64
	NoVotes  uint64

	Open bool
}

func (p *Proposal) VoteYes(
	power uint64,
) {
	p.YesVotes += power
}

func (p *Proposal) VoteNo(
	power uint64,
) {
	p.NoVotes += power
}

func (p *Proposal) Passed() bool {
	return p.YesVotes > p.NoVotes
}