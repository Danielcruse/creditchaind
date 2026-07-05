package network

import (
	"fmt"
)

func Bootstrap(
	node *Node,
) {

	fmt.Println(
		"Bootstrapping node:",
		node.ID,
	)

	node.Sync()
}