// Package argon2id реализует Argon2id (RFC 9106, версия 0x13).
//
// Зачем свой пакет: golang.org/x/crypto/argon2 не входит в vendor, а загрузка
// модулей закрыта. Реализация следует референсной (RFC 9106, раздел 3),
// проверяется официальным вектором RFC и независимой реализацией
// (Python, библиотека cryptography). При появлении x/crypto её стоит
// предпочесть: этот пакет нужно заменить правкой одной функции Key.
//
// Полосы (parallelism) вычисляются последовательно: результат тот же, что у
// параллельной реализации, только медленнее при threads больше единицы.
package argon2id

import (
	"encoding/binary"
	"math/bits"
)

const (
	version       = 0x13
	typeArgon2id  = 2
	blockWords    = 128 // блок 1024 байта
	syncPoints    = 4
	addressesInBl = 128
)

type block [blockWords]uint64

// Key возвращает keyLen байт Argon2id. memoryKiB не меньше 8*threads.
// password и salt не изменяются; всё промежуточное затирается.
// secret и ad (ключ и ассоциированные данные RFC 9106) необязательны.
func Key(password, salt, secret, ad []byte, time, memoryKiB uint32, threads uint8, keyLen uint32) []byte {
	if time < 1 || threads < 1 || memoryKiB < 8*uint32(threads) || keyLen < 4 || uint64(len(password)) > uint64(^uint32(0)) || uint64(len(salt)) > uint64(^uint32(0)) || uint64(len(secret)) > uint64(^uint32(0)) || uint64(len(ad)) > uint64(^uint32(0)) {
		panic("argon2id: invalid parameters")
	}
	p := uint32(threads)
	// m' = 4p * floor(m / 4p)
	memBlocks := memoryKiB / (4 * p) * (4 * p)
	laneLen := memBlocks / p
	segLen := laneLen / syncPoints

	h0 := initialHash(password, salt, secret, ad, p, keyLen, memoryKiB, time)
	defer clear(h0)

	mem := make([]block, memBlocks)
	defer func() {
		for i := range mem {
			clear(mem[i][:])
		}
	}()
	for lane := uint32(0); lane < p; lane++ {
		for i := uint32(0); i < 2; i++ {
			in := make([]byte, 0, 72)
			in = append(in, h0...)
			in = binary.LittleEndian.AppendUint32(in, i)
			in = binary.LittleEndian.AppendUint32(in, lane)
			bytesToBlock(&mem[lane*laneLen+i], longHash(1024, in))
			clear(in)
		}
	}

	for pass := uint32(0); pass < time; pass++ {
		for slice := uint32(0); slice < syncPoints; slice++ {
			for lane := uint32(0); lane < p; lane++ {
				fillSegment(mem, pass, slice, lane, p, laneLen, segLen, memBlocks, time)
			}
		}
	}

	var final block
	defer clear(final[:])
	for lane := uint32(0); lane < p; lane++ {
		last := &mem[lane*laneLen+laneLen-1]
		for i := range final {
			final[i] ^= last[i]
		}
	}
	var buf [1024]byte
	blockToBytes(buf[:], &final)
	defer clear(buf[:])
	return longHash(keyLen, buf[:])
}

func initialHash(password, salt, secret, ad []byte, p, keyLen, m, t uint32) []byte {
	var parts [][]byte
	u32 := func(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
	parts = append(parts, u32(p), u32(keyLen), u32(m), u32(t), u32(version), u32(typeArgon2id))
	for _, f := range [][]byte{password, salt, secret, ad} {
		parts = append(parts, u32(uint32(len(f))) //nolint:gosec // Key rejects inputs larger than the Argon2 uint32 length fields., f)
	}
	return blake2b(64, parts...)
}

// longHash это H' из RFC 9106: BLAKE2b с произвольной длиной результата.
func longHash(size uint32, in []byte) []byte {
	prefix := binary.LittleEndian.AppendUint32(nil, size)
	if size <= 64 {
		return blake2b(int(size), prefix, in)
	}
	r := (size+31)/32 - 2
	out := make([]byte, 0, size)
	v := blake2b(64, prefix, in)
	out = append(out, v[:32]...)
	for i := uint32(1); i < r; i++ {
		next := blake2b(64, v)
		clear(v)
		v = next
		out = append(out, v[:32]...)
	}
	last := blake2b(int(size-32*r), v)
	clear(v)
	out = append(out, last...)
	clear(last)
	return out
}

func bytesToBlock(b *block, in []byte) {
	for i := range b {
		b[i] = binary.LittleEndian.Uint64(in[8*i:])
	}
	clear(in)
}

func blockToBytes(out []byte, b *block) {
	for i, v := range b {
		binary.LittleEndian.PutUint64(out[8*i:], v)
	}
}

func fillSegment(mem []block, pass, slice, lane, lanes, laneLen, segLen, memBlocks, passes uint32) {
	dataIndependent := pass == 0 && slice < syncPoints/2
	var input, addr, zero block
	defer clear(input[:])
	defer clear(addr[:])
	if dataIndependent {
		input[0], input[1], input[2] = uint64(pass), uint64(lane), uint64(slice)
		input[3], input[4], input[5] = uint64(memBlocks), uint64(passes), typeArgon2id
	}

	start := uint32(0)
	if pass == 0 && slice == 0 {
		start = 2
		if dataIndependent {
			nextAddresses(&addr, &input, &zero)
		}
	}

	cur := lane*laneLen + slice*segLen + start
	var prev uint32
	if cur%laneLen == 0 {
		prev = cur + laneLen - 1
	} else {
		prev = cur - 1
	}
	for i := start; i < segLen; i, cur, prev = i+1, cur+1, prev+1 {
		if cur%laneLen == 1 {
			prev = cur - 1
		}
		var rnd uint64
		if dataIndependent {
			if i%addressesInBl == 0 {
				nextAddresses(&addr, &input, &zero)
			}
			rnd = addr[i%addressesInBl]
		} else {
			rnd = mem[prev][0]
		}
		refLane := uint32(rnd>>32) % lanes
		if pass == 0 && slice == 0 {
			refLane = lane
		}
		refIdx := indexAlpha(pass, slice, i, refLane == lane, uint32(rnd & 0xffffffff), laneLen, segLen)
		compress(&mem[cur], &mem[prev], &mem[refLane*laneLen+refIdx], pass > 0)
	}
}

func nextAddresses(addr, input, zero *block) {
	input[6]++
	compress(addr, zero, input, false)
	compress(addr, zero, addr, false)
}

func indexAlpha(pass, slice, index uint32, sameLane bool, rnd32, laneLen, segLen uint32) uint32 {
	var area uint32
	switch {
	case pass == 0 && slice == 0:
		area = index - 1
	case pass == 0 && sameLane:
		area = slice*segLen + index - 1
	case pass == 0:
		area = slice * segLen
		if index == 0 {
			area--
		}
	case sameLane:
		area = laneLen - segLen + index - 1
	default:
		area = laneLen - segLen
		if index == 0 {
			area--
		}
	}
	rel := uint64(rnd32)
	rel = rel * rel >> 32
	rel = uint64(area) - 1 - (uint64(area) * rel >> 32)
	startPos := uint32(0)
	if pass != 0 && slice != syncPoints-1 {
		startPos = (slice + 1) * segLen
	}
	return uint32(((uint64(startPos) + rel) % uint64(laneLen)) & 0xffffffff)
}

// compress это G из RFC 9106: dst = P(prev xor ref) xor prev xor ref,
// при xorDst результат дополнительно складывается с прежним dst.
func compress(dst, prev, ref *block, xorDst bool) {
	var r, z block
	for i := range r {
		r[i] = prev[i] ^ ref[i]
	}
	z = r
	for i := 0; i < 8; i++ {
		permute(&z, [16]int{16 * i, 16*i + 1, 16*i + 2, 16*i + 3, 16*i + 4, 16*i + 5, 16*i + 6, 16*i + 7,
			16*i + 8, 16*i + 9, 16*i + 10, 16*i + 11, 16*i + 12, 16*i + 13, 16*i + 14, 16*i + 15})
	}
	for i := 0; i < 8; i++ {
		permute(&z, [16]int{2 * i, 2*i + 1, 2*i + 16, 2*i + 17, 2*i + 32, 2*i + 33, 2*i + 48, 2*i + 49,
			2*i + 64, 2*i + 65, 2*i + 80, 2*i + 81, 2*i + 96, 2*i + 97, 2*i + 112, 2*i + 113})
	}
	for i := range dst {
		v := z[i] ^ r[i]
		if xorDst {
			v ^= dst[i]
		}
		dst[i] = v
	}
	clear(r[:])
	clear(z[:])
}

func permute(b *block, idx [16]int) {
	g := func(a, bb, c, d int) {
		ia, ib, ic, id := idx[a], idx[bb], idx[c], idx[d]
		b[ia] = b[ia] + b[ib] + 2*(b[ia]&0xffffffff)*(b[ib]&0xffffffff)
		b[id] = bits.RotateLeft64(b[id]^b[ia], -32)
		b[ic] = b[ic] + b[id] + 2*(b[ic]&0xffffffff)*(b[id]&0xffffffff)
		b[ib] = bits.RotateLeft64(b[ib]^b[ic], -24)
		b[ia] = b[ia] + b[ib] + 2*(b[ia]&0xffffffff)*(b[ib]&0xffffffff)
		b[id] = bits.RotateLeft64(b[id]^b[ia], -16)
		b[ic] = b[ic] + b[id] + 2*(b[ic]&0xffffffff)*(b[id]&0xffffffff)
		b[ib] = bits.RotateLeft64(b[ib]^b[ic], -63)
	}
	g(0, 4, 8, 12)
	g(1, 5, 9, 13)
	g(2, 6, 10, 14)
	g(3, 7, 11, 15)
	g(0, 5, 10, 15)
	g(1, 6, 11, 12)
	g(2, 7, 8, 13)
	g(3, 4, 9, 14)
}
