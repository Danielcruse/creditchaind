// erc20-abi.js - minimal ERC-20 calldata encoding, transfer() only. No
// general ABI library needed for one fixed, well-known function
// signature: contract calldata is just a 4-byte function selector
// (the first 4 bytes of keccak256(functionSignature)) followed by each
// argument left-padded to a 32-byte word. Depends on hd-crypto.js
// (hexToBytes) being loaded first, same as eth-tx.js.
(function (global) {
    "use strict";

    const TRANSFER_SELECTOR = "a9059cbb"; // keccak256("transfer(address,uint256)")[:4]

    function pad32(hex) {
        return hex.padStart(64, "0");
    }

    // erc20TransferCalldata builds the raw calldata for
    // transfer(to, amount) - amount is the token's SMALLEST unit (e.g.
    // USDC has 6 decimals), as a BigInt or a value BigInt() accepts.
    function erc20TransferCalldata(toAddressHex, amountSmallestUnits) {
        const toHex = toAddressHex.replace(/^0x/i, "").toLowerCase();
        if (toHex.length !== 40) {
            throw new Error("`to` must be a 20-byte (0x-prefixed 40-hex-char) Ethereum address");
        }
        const amountHex = BigInt(amountSmallestUnits).toString(16);
        const calldataHex = TRANSFER_SELECTOR + pad32(toHex) + pad32(amountHex);
        return HDCrypto.hexToBytes(calldataHex);
    }

    global.Erc20Abi = { erc20TransferCalldata };
})(window);
