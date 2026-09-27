// Package deltaencode 把递增整数序列编成差分 + 变长字节。
package deltaencode

import "encoding/binary"

// Encode 编码。缺陷：每个差分固定写 8 字节，且没做 zigzag。
func Encode(values []int64) []byte {
	out := make([]byte, 0, len(values)*8)
	previous := int64(0)
	for _, value := range values {
		delta := value - previous
		previous = value
		var buffer [8]byte
		binary.BigEndian.PutUint64(buffer[:], uint64(delta))
		out = append(out, buffer[:]...)
	}
	return out
}

// Decode 解码。缺陷：按固定 8 字节读，也不做 zigzag 还原。
func Decode(data []byte) ([]int64, error) {
	if len(data)%8 != 0 {
		return nil, ErrLength
	}
	out := []int64{}
	previous := int64(0)
	for at := 0; at < len(data); at += 8 {
		delta := int64(binary.BigEndian.Uint64(data[at : at+8]))
		previous += delta
		out = append(out, previous)
	}
	return out, nil
}

// ErrLength 表示数据长度不是 8 的倍数。
var ErrLength = errLength("数据长度不是 8 的倍数")

type errLength string

func (e errLength) Error() string { return string(e) }
