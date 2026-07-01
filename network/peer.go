package network

type Peer struct {
	ID      string
	Address string
	Active  bool
}

type Node struct {
	ID    string
	Peers []Peer
}

func NewNode(
	id string,
) *Node {

	return &Node{
		ID:    id,
		Peers: []Peer{},
	}
}

func (n *Node) AddPeer(
	peer Peer,
) {
	n.Peers =
		append(
			n.Peers,
			peer,
		)
}