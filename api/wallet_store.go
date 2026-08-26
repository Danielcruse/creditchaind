package api

import (
	"github.com/creditchain/creditchaind/core"
)

// walletStore is the canonical, address-keyed wallet registry.
// Wallet.Reputation is the single source of truth for reputation
// across the whole node - every handler that needs an address's
// reputation should go through this, not a separate store.
var walletStore = core.NewWalletStore()
