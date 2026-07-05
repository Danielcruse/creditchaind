package network

import "fmt"

func DiscoverPeers(
	node *Node,
) {

	fmt.Printf(
		"Discovering peers for %s\n",
		node.ID,
	)
}