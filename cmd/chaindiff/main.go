package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

type Block struct {
	Index          uint64
	Hash           string
	PreviousHash   string
	ProducerNodeID string
}

func fetch(url string) []Block {
	resp, err := http.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var blocks []Block
	json.NewDecoder(resp.Body).Decode(&blocks)
	return blocks
}

func main() {
	a := fetch(os.Args[1])
	b := fetch(os.Args[2])

	fmt.Println("A len:", len(a), "B len:", len(b))

	n := len(a)
	if len(b) < n {
		n = len(b)
	}

	diverged := -1
	for i := 0; i < n; i++ {
		if a[i].Hash != b[i].Hash {
			diverged = i
			break
		}
	}

	if diverged == -1 {
		fmt.Println("No divergence - B is a clean prefix of A (or vice versa) up to index", n-1)
	} else {
		fmt.Println("DIVERGED at index", diverged)
		fmt.Println("A:", a[diverged].Hash, "producer", a[diverged].ProducerNodeID)
		fmt.Println("B:", b[diverged].Hash, "producer", b[diverged].ProducerNodeID)
	}
}
