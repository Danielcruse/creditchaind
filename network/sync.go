package network

import "fmt"

func (n *Node) Sync() {
	fmt.Printf(
		"Node %s syncing with %d peers...\n",
		n.ID,
		len(n.Peers),
	)
}