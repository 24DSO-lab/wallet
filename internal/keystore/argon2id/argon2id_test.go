package argon2id

import (
	"encoding/hex"
	"testing"
)

// Эталоны: первая запись это вектор Argon2id из RFC 9106 (с secret и ad);
// остальные и все BLAKE2b получены независимыми реализациями (cryptography
// поверх OpenSSL, hashlib). Покрыты полосы 1..8, несколько проходов и
// память, не кратная 4p.
var argonVectors = []struct {
	pw, salt, secret, ad string
	t, m                 uint32
	p                    uint8
	n                    uint32
	want                 string
}{
	{"0101010101010101010101010101010101010101010101010101010101010101", "02020202020202020202020202020202", "0303030303030303", "040404040404040404040404", 3, 32, 4, 32, "0d640df58d78766c08c037a34a8b53c9d01ef0452d75b65eb52520e96b01e659"}, // RFC 9106, раздел 5.3
	{"070a0d101316191c1f222528", "070c11161b20252a2f34393e43484d52", "", "", 1, 8, 1, 32, "6a628e1ccd2e2437ad445520ff30acebd22c2e18ce05c220c68f247077c31734"},
	{"070a0d101316191c1f2225282b", "070c11161b20252a2f34393e43484d52", "", "", 2, 64, 1, 32, "d2001fe92860df5fdbdf04dcda4ad1c638790b08e1a4488d41fa747130b3ff34"},
	{"070a0d101316191c1f2225282b2e", "070c11161b20252a2f34393e43484d52", "", "", 3, 64, 2, 32, "178d636f7203344671d625b92ce61d74300a66f6ee060a1e7f26bf5af5ce25b1"},
	{"070a0d101316191c1f222528", "070c11161b20252a2f34393e43484d52", "", "", 1, 128, 4, 16, "d69c38b654beae9a55cfca1781995bc7"},
	{"070a0d101316191c1f2225282b2e31", "070c11161b20252a2f34393e43484d52", "", "", 4, 256, 3, 64, "2c58390a3ca1624343cecd484f8f22d03098f9880adb1b464aa8e8bacae29170619d5bcdc4b2513a9b6cd375248c3386b5740f3f5f3c2c7bb2c430ce6eab7aa0"},
	{"070a0d101316191c1f2225282b", "070c11161b20252a2f34393e43484d52", "", "", 2, 19456, 1, 32, "86c8cb80b56be31136b75d69bd8ebbb28184cb2f9993a57ba7c2012cd9e02182"},
	{"070a0d101316191c1f2225282b2e", "070c11161b20252a2f34393e43484d52", "", "", 3, 1000, 1, 100, "5c305cc4e802936bac30119366c5d6ded35850bd282efaf0a58872a0001bd4619821561e9023ee4a4cf76919d00a7ad60dcd7c9c5b647c64b9b916500642662769ab2a0d388887c9ce0ada17ed2761cd9a5fee9473127f6c921d7ac4e232a368eac3a768"},
	{"070a0d101316191c1f222528", "070c11161b20252a2f34393e43484d52", "", "", 1, 96, 2, 1032, "6f611637e533ac6efc153c7c212dcb3ebcac30bc07e355c1ff69489b3afc9e0ecee28b4fefc9c1541783170920e1f85844900fabc3316c55ccb9b0ec1d9a4edd5ce14c34d9cb4aa28b6797edb7efca585b1b77d8be21e808e517afa35cd122a6e3014ef38d56409ff39437dceaf9297931c70d5133a39c68da53d0607f4a4fa4d94b7f540bb8bc873bc59fa129cf7c71d63029c6be695eccbb37a583623132f578eed72e4e56dd43df5a64b019c9877eb7c121d1bfd2b0de75f638d6ff8d725cb5eaa2ed4fb66c65a027f1692423dd37430526d9c015f2b3563e79818729c2f79ea145619a0558a950529d1e19aa87e75bebe4935d261b2e4d3b1c711f41d66c27175d849ca28360b24b7a3162ea29f8ebfbcd3295071d34de40e778c23435a29237721ec26dc2ccf181af6c3de8dc5ed25e13318ef77ad24413f2d86af23f0388ee7e4f76c7d07944dd52f1edcd62e6f6625ea59393c954a8ca6ddd28ff14d46307dbc894909de06395c5c0c08d380cb0e77242f5909aefb2b96300c1b91d1582650b505093cfb66c2463dabd4355801f3df4a6a2e65388beceb8184c25fac9e74236af7764647fa96e598e1a78f4bc8776a217206efe2b6f312fcef9015150b087d09ae7b4e3db17385bff4dae487ce5196f1cb951e62ed9219bcdb3a5bbc9ba18c814e263a70c9ad437d68ecd5e6690605a1a8345c1d24b7d1484bbcfa804bfebc95e71ae1d92460f9209f809c777d9d5618c200c02c25776e2de3280705adb25e0626e0298f5aefbf8d3053f00795a3669f3ff078b0a2445f82892b8e028e727a3f09cd03450764bc8ec3006ffcc04156a2a297166735788bbb19aec7bf1885b22ad81e28f226885277f3f7f37e228cd0f0c73539ad2ba4e12606082e8cf64b1dfdcc54136b46147e2ea2b4ddec4b0a149a6a26aafb747badd12cc537771c2d2fca49b8fddbbb2e2378ed861358d4848a202da503f084016b38b8f391ae7cb662adc8da5195edcf25b5f4ada14278be3188178010a01584fa387f469cec4bc235399bba1557416577b7d74253a6b4c5712ad35bf49169fd1062f9fed22a6b4c4eb36f04a84b77f70843031047d673d7cddf04fb2e956576947750b1ae3e4963ba95ef2e038b6cec516ba4071e3db7b6fd719b089b08c353b4103e344532c0dae851139c1a7640f4546a001726531cdd1675830a98208091c474fdb6123443072051cfee06ba5b8fc88d95c26c1781e76c5d83b298eafee2d88efb0efb7b912c329a03105f374773e1ce9d5d3f2e6ee9a91133a34d208e8b9bebaf756638ecdfd40364f2f3b84fee2bdf5302f6f1b62fa4994dae5f4e30ecd4993a8308ba74aeb25445b1e26842ae8fbe1b53f4ed5e0b34109fd970f62c835a233eb21bf61571ce2dc1bef1459b88276b3285cbc2bd64b22738d92cffdbe89f9f9b7bea37c6d4098238c37951c"},
	{"070a0d101316191c1f2225282b", "070c11161b20252a2f34393e43484d52", "", "", 2, 33, 1, 32, "948c123ffc1fa10c67d21a44d11e9214df32f2e8e0fb42f38f6e913b350c8fc7"},
	{"070a0d101316191c1f2225282b2e3134", "070c11161b20252a2f34393e43484d52", "", "", 5, 512, 8, 48, "aadfca50f318246254dac10559bfe1eaaa92ae43363e9b1e5e89ae5f5720f2c41ac41b5142982ad60114862d45b2395f"},
}

var blake2bVectors = []struct {
	n, size int
	want    string
}{
	{0, 1, "2e"},
	{0, 20, "3345524abf6bbe1809449224b5972c41790b6cf2"},
	{0, 32, "0e5751c026e543b2e8ab2eb06099daa1d1e5df47778f7787faab45cdf12fe3a8"},
	{0, 64, "786a02f742015903c6c6fd852552d272912f4740e15847618a86e217f71f5419d25e1031afee585313896444934eb04b903a685b1448b755d56f701afe9be2ce"},
	{1, 1, "be"},
	{1, 20, "ca5d3fa0a6887285ef6aa85cb12960a2b6706e00"},
	{1, 32, "ee155ace9c40292074cb6aff8c9ccdd273c81648ff1149ef36bcea6ebb8a3e25"},
	{1, 64, "9545ba37b230d8a2e716c4707586542780815b7c4088edcb9af6a9452d50f32474d5ba9aab52a67aca864ef2696981c2eadf49020416136afd838fb048d21653"},
	{127, 1, "26"},
	{127, 20, "4ba8bee4b7a6b340e8d1001b98be29159ea54d52"},
	{127, 32, "56cc872e96a8407bf5a7d3f9c65eea8b2842201f8b6791409ff97761e8a53ea2"},
	{127, 64, "a538fe3d8a41d94fa8bff46eea4b8fae45070c5ac58c0630ec5b15488eab5f4036e31b910174d0aea09d29d0c96cd9f05445700b677a8b5e786a4e306551a9f9"},
	{128, 1, "9e"},
	{128, 20, "631a6270dd1f32c3034ffcb7d917bd8cd88fac61"},
	{128, 32, "5793c37b09d82cee2c686f7e0a062129a42234f3085d00741b7e381d05a31425"},
	{128, 64, "b7593b191dece2474891a19d97481e9911bf08eec854de42e19bee4654a230b45a5d90abe315911e773c1a88071767c0e96e29c480cec72c447c37e663bd4a64"},
	{129, 1, "0f"},
	{129, 20, "abeb53ab09e1815f9d6abaa2502d56aca1c32359"},
	{129, 32, "573369973fa0c51fc586b7919728f52a76bbbb2f3137de4e9bbffd98112da82c"},
	{129, 64, "e08c10bdcf15acb857dc9e2b8142e363427a1f596256112c248fbbab9b85ec81408eb5cec5ae588d991bd0885bec74b79ffb9e6d2be9071cfa44c119d1e75651"},
	{255, 1, "ac"},
	{255, 20, "959f190be538af6ee566b28fd5f7d12cf175e3b8"},
	{255, 32, "34504e81ca95f25463d18564686a3b4609e22485aa5286d3ac81870ab7156581"},
	{255, 64, "cfdfe5ef294c963e02f906502b46a916a8bdc6781b11d5d456bd09fda39f5e9ef8854df18ae12d840013ef14c838cf0490c5259df2e30c21b81ddecd4673da68"},
	{256, 1, "c3"},
	{256, 20, "24fcc13715adfc991f93bc6148cb3be8b1d50c08"},
	{256, 32, "95c941115d89cef6e6da01cfea218f6457eb752acf95814daf351930c38d2f1f"},
	{256, 64, "49a38f6679dfc59e0619fb3277cee81ee3316cb49c650b4ae38450a26269ed70324966528268defeb00dcf79622af1ca05926bf79e0735612c3a986d26cd66a4"},
	{257, 1, "ee"},
	{257, 20, "161265505db5b2277435c431df1e99c3f886ff1c"},
	{257, 32, "a037159e4fcece584690e08dd41591769dcd3c784e2bd36178aafcbe2aa35fc9"},
	{257, 64, "1c13be5af4cdcb41b6db1b351b2cf06765cdbd68cc37f8ac7fd97f6741bf823e31b3c2b8ccf3c4ba20c9fcb2d4c52a29d0a677bbf666abae1741c1ecee932abd"},
	{1000, 1, "5a"},
	{1000, 20, "c75f8e9599f7d5692b37993c6b54db99652aa429"},
	{1000, 32, "2f56754e74b77621b59ce768b7d27b1d06be64bc6816f6241d144e490251febc"},
	{1000, 64, "60e60a4b9f52ca864507ae0c92b52e97f005130443fd4557359033d82d506beacc0cc752fb828e247c8878bba264a8d8903123465bdbed1eccc5dbf130b7f1b8"},
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestArgon2idVectors(t *testing.T) {
	for _, v := range argonVectors {
		got := Key(mustHex(t, v.pw), mustHex(t, v.salt), mustHex(t, v.secret), mustHex(t, v.ad), v.t, v.m, v.p, v.n)
		if hex.EncodeToString(got) != v.want {
			t.Errorf("t=%d m=%d p=%d n=%d: got %x, want %s", v.t, v.m, v.p, v.n, got, v.want)
		}
	}
}

func TestBlake2bVectors(t *testing.T) {
	for _, v := range blake2bVectors {
		in := make([]byte, v.n)
		for i := range in {
			in[i] = byte((i*13 + 1) % 256)
		}
		// Разбиение входа на части не должно менять результат.
		half := v.n / 2
		got := blake2b(v.size, in[:half], in[half:])
		if hex.EncodeToString(got) != v.want {
			t.Errorf("len %d size %d: got %x, want %s", v.n, v.size, got, v.want)
		}
	}
}

func TestKeyDoesNotModifyInputs(t *testing.T) {
	pw, salt := []byte("password"), []byte("saltsaltsaltsalt")
	pw0, salt0 := string(pw), string(salt)
	Key(pw, salt, nil, nil, 1, 64, 1, 32)
	if string(pw) != pw0 || string(salt) != salt0 {
		t.Error("Key changed its inputs")
	}
}

func TestKeyRejectsBadParameters(t *testing.T) {
	for name, f := range map[string]func(){
		"time":    func() { Key(nil, nil, nil, nil, 0, 64, 1, 32) },
		"threads": func() { Key(nil, nil, nil, nil, 1, 64, 0, 32) },
		"memory":  func() { Key(nil, nil, nil, nil, 1, 15, 2, 32) },
		"length":  func() { Key(nil, nil, nil, nil, 1, 64, 1, 3) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}

func BenchmarkKey64MiB(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Key([]byte("password"), []byte("saltsaltsaltsalt"), nil, nil, 3, 64*1024, 1, 32)
	}
}
