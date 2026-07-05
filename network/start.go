package network

func StartNode(
	id string,
) *Node {

	node :=
		NewNode(id)

	Bootstrap(
		node,
	)

	DiscoverPeers(
		node,
	)

	return node
}