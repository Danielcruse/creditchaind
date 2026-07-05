package core

type ProfileStore struct {
	Profiles map[string]*Reputation
}

func NewProfileStore() *ProfileStore {
	return &ProfileStore{
		Profiles: make(map[string]*Reputation),
	}
}

func (ps *ProfileStore) Get(
	address string,
) *Reputation {

	profile, ok := ps.Profiles[address]

	if !ok {
		rep := NewReputation(address)
		ps.Profiles[address] = &rep
		return &rep
	}

	return profile
}