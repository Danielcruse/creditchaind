package main

import (
	"github.com/creditchain/creditchaind/api"
         
)


func main() {

	api.LoadBlockchain()

	api.StartServer(
		":8080",
	)
}