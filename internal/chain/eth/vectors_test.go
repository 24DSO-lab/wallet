package eth

// Векторы получены независимой реализацией (Keccak-256, RLP и secp256k1 на чистом
// Python, без общего кода с адаптером): ключ 1 или 2, детерминированная подпись
// с канонической s, v проверен восстановлением ключа. Адрес отправителя
// совпадает с общеизвестным адресом ключа.
type txVector struct {
	name                 string
	chainID, nonce, gas  uint64
	prio, maxFee, value  string // десятичные, wei
	to, data             string
	unsigned, digest     string
	r, s                 string
	v                    byte
	signed, txid, sender string
}

var txVectors = []txVector{
	{
		name: "eth_sepolia", chainID: 11155111, nonce: 7, gas: 21000,
		prio: "1000000000", maxFee: "31000000000", value: "100000000000000000",
		to: "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359", data: "",
		unsigned: "02f383aa36a707843b9aca00850737be760082520894fb6916095ca1df60bb79ce92ce3ea74c37c5d35988016345785d8a000080c0",
		digest:   "caad3fde7106d5203be77b00e549478c4cb110d7d7b23df6463a1c400bf1ba8f",
		r:        "cdfe0518663e79a141a4a4d58a1eeb2ffde4e21bb9ada3333998c289e5499de1",
		s:        "4e9b0d1f1a0cff293a87a5583dc643849e530bda8011e58fafe157060ed0e566",
		v:        0,
		signed:   "02f87683aa36a707843b9aca00850737be760082520894fb6916095ca1df60bb79ce92ce3ea74c37c5d35988016345785d8a000080c080a0cdfe0518663e79a141a4a4d58a1eeb2ffde4e21bb9ada3333998c289e5499de1a04e9b0d1f1a0cff293a87a5583dc643849e530bda8011e58fafe157060ed0e566",
		txid:     "0x0333c7cb0d3b6d63ee8f63f5ffdfc4ea4a2389deefd049d7c4454bb5ec09ebde",
		sender:   "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf",
	},
	{
		name: "usdc_sepolia", chainID: 11155111, nonce: 8, gas: 60000,
		prio: "1000000000", maxFee: "31000000000", value: "0",
		to: "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238", data: "a9059cbb0000000000000000000000005aaeb6053f3e94c9b9a09f33669435e7ef1beaed000000000000000000000000000000000000000000000000000000000016e360",
		unsigned: "02f87083aa36a708843b9aca00850737be760082ea60941c7d4b196cb0c7b01d743fbc6116a902379c723880b844a9059cbb0000000000000000000000005aaeb6053f3e94c9b9a09f33669435e7ef1beaed000000000000000000000000000000000000000000000000000000000016e360c0",
		digest:   "44ef4ab265808fbe687e06d4b40993c2d0b42a6399e0190271bda6a3b72ca033",
		r:        "f79d318c5942217516d77606e56dfa452a0d7bd866c0be1578855b3688825f58",
		s:        "7ae04f53ecb7fde68eeb0f9ffc1fdea834d094bd64917fd7d3419d0e81f54f1e",
		v:        0,
		signed:   "02f8b383aa36a708843b9aca00850737be760082ea60941c7d4b196cb0c7b01d743fbc6116a902379c723880b844a9059cbb0000000000000000000000005aaeb6053f3e94c9b9a09f33669435e7ef1beaed000000000000000000000000000000000000000000000000000000000016e360c080a0f79d318c5942217516d77606e56dfa452a0d7bd866c0be1578855b3688825f58a07ae04f53ecb7fde68eeb0f9ffc1fdea834d094bd64917fd7d3419d0e81f54f1e",
		txid:     "0xbd41851c9c66f490d78eebe5c094336e5bf06168b72e9b22a414a2f7bc4b9911",
		sender:   "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf",
	},
	{
		name: "eth_mainnet_zero_nonce", chainID: 1, nonce: 0, gas: 21000,
		prio: "0", maxFee: "1000000000", value: "0",
		to: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", data: "",
		unsigned: "02e3018080843b9aca00825208945aaeb6053f3e94c9b9a09f33669435e7ef1beaed8080c0",
		digest:   "1a4d74eb777ee64e5c9afc88d57b30917c63e936d38929ac898b18460bf19e6a",
		r:        "24ca323fccdc976fc9beda6de948012ec9822874c64d05c1d45e92128b4e931c",
		s:        "33554f55bb8f658d3cb5e80e7fda2c47f00cd2e0436ceae599fd4a663fdbf938",
		v:        0,
		signed:   "02f866018080843b9aca00825208945aaeb6053f3e94c9b9a09f33669435e7ef1beaed8080c080a024ca323fccdc976fc9beda6de948012ec9822874c64d05c1d45e92128b4e931ca033554f55bb8f658d3cb5e80e7fda2c47f00cd2e0436ceae599fd4a663fdbf938",
		txid:     "0xe576e44467dd1ca57be0df9ec32c0cbc98f52ecac449d09bcf87d39d91331cfc",
		sender:   "0x2b5ad5c4795c026514f8317c7a215e218dccd6cf",
	},
}
