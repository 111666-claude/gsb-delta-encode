// Package deltaencode 把整数序列编成「分帧 + 差分 + zigzag/varint」字节流，只用标准库。
//
// 线格式（多字节整数均为大端）：
//
//	空输入 => 空字节流
//	头部:  magic "DLT1"(4) | count uint64(8) | groupCount uint32(4)
//	       | groupCount 个组帧绝对偏移 uint64
//	组帧:  组内首值绝对值(zigzag+LEB128)
//	       后跟 (组大小-1) 个差分(zigzag+LEB128)
//	       末尾 CRC32-IEEE(4)，覆盖组头与全部差分
//
// 每组固定 GroupSize 项（最后一组可不足），偏移表让随机访问做到
// O(1) 组定位 + O(组大小) 解码。
package deltaencode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// GroupSize 是每一帧包含的项数上限。
const GroupSize = 128

var magic = [4]byte{'D', 'L', 'T', '1'}

const (
	headerSize     = 4 + 8 + 4
	crcTrailerSize = 4
)

// DecodeError 携带出错字节偏移与所在组（从 0 起；不适用时为 -1）。
type DecodeError struct {
	Offset int64
	Group  int
	msg    string
}

func (e *DecodeError) Error() string {
	switch {
	case e.Group >= 0 && e.Offset >= 0:
		return fmt.Sprintf("%s（组 %d，偏移 %d）", e.msg, e.Group, e.Offset)
	case e.Offset >= 0:
		return fmt.Sprintf("%s（偏移 %d）", e.msg, e.Offset)
	default:
		return e.msg
	}
}

func decodeError(offset int64, group int, format string, args ...any) error {
	return &DecodeError{Offset: offset, Group: group, msg: fmt.Sprintf(format, args...)}
}

// ZigZagEncode 把有符号 int64 映射成无符号 uint64：小绝对值（含负差分）变小正数。
func ZigZagEncode(value int64) uint64 {
	return uint64(value<<1) ^ uint64(value>>63)
}

// ZigZagDecode 是 ZigZagEncode 的逆映射。
func ZigZagDecode(encoded uint64) int64 {
	return int64(encoded>>1) ^ -int64(encoded&1)
}

// appendVarint 追加一个 LEB128 无符号变长整数（最多 10 字节）。
func appendVarint(out []byte, value uint64) []byte {
	var buffer [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buffer[:], value)
	return append(out, buffer[:n]...)
}

// readVarint 从 buf 读取一个 LEB128。base 是 buf 在整个字节流中的起始偏移，
// 便于报错定位。截断、超过 10 字节、累积溢出一律报 DecodeError。
func readVarint(buf []byte, base int64) (uint64, int, error) {
	var value uint64
	for index, b := range buf {
		if index == binary.MaxVarintLen64 {
			return 0, index, decodeError(base+int64(index), -1, "varint 超过 %d 字节", binary.MaxVarintLen64)
		}
		// 第 10 字节只允许最低位承载数据，否则 64 位溢出。
		if index == binary.MaxVarintLen64-1 && b&0x7f > 1 {
			return 0, index, decodeError(base+int64(index), -1, "varint 累积溢出")
		}
		value |= uint64(b&0x7f) << (7 * index)
		if b&0x80 == 0 {
			return value, index + 1, nil
		}
	}
	return 0, len(buf), decodeError(base+int64(len(buf)), -1, "varint 截断")
}

// groupCountFor 返回 count 项需要的组数。
func groupCountFor(count int64) uint32 {
	return uint32((count + int64(GroupSize) - 1) / int64(GroupSize))
}

// Encode 按线格式编码。同一输入逐字节确定；空输入（nil 或长度 0）编成空。
// 额外内存为 O(输出字节)：每组只在偏移表中占 8 字节。
func Encode(values []int64) []byte {
	if len(values) == 0 {
		return []byte{}
	}

	count := int64(len(values))
	groups := groupCountFor(count)
	out := make([]byte, headerSize+8*int(groups))
	copy(out, magic[:])
	binary.BigEndian.PutUint64(out[4:], uint64(count))
	binary.BigEndian.PutUint32(out[12:], uint32(groups))

	for g := 0; g < int(groups); g++ {
		begin := g * GroupSize
		end := begin + GroupSize
		if end > len(values) {
			end = len(values)
		}

		frameStart := int64(len(out))
		binary.BigEndian.PutUint64(out[headerSize+8*g:], uint64(frameStart))

		previous := int64(0)
		for index := begin; index < end; index++ {
			if index == begin {
				out = appendVarint(out, ZigZagEncode(values[index]))
			} else {
				out = appendVarint(out, ZigZagEncode(values[index]-previous))
			}
			previous = values[index]
		}

		checksum := crc32.ChecksumIEEE(out[frameStart:])
		var trailer [crcTrailerSize]byte
		binary.BigEndian.PutUint32(trailer[:], checksum)
		out = append(out, trailer[:]...)
	}
	return out
}

type header struct {
	count    int64
	groups   uint32
	tableEnd int64
}

func parseHeader(data []byte) (header, error) {
	if len(data) == 0 {
		return header{}, nil
	}
	if len(data) < headerSize {
		return header{}, decodeError(0, -1, "头部过短：需要至少 %d 字节，实际 %d", headerSize, len(data))
	}
	if string(data[:4]) != string(magic[:]) {
		return header{}, decodeError(0, -1, "magic 不匹配，不是本格式数据")
	}

	count := int64(binary.BigEndian.Uint64(data[4:]))
	groups := binary.BigEndian.Uint32(data[12:])
	if count <= 0 {
		return header{}, decodeError(4, -1, "头部声明的项数为 %d，非空数据项数必须为正", count)
	}
	if want := groupCountFor(count); groups != want {
		return header{}, decodeError(12, -1, "头部声明组数 %d 与项数 %d 推导出的组数 %d 不符", groups, count, want)
	}

	tableEnd := int64(headerSize) + 8*int64(groups)
	if int64(len(data)) < tableEnd {
		return header{}, decodeError(int64(len(data)), -1, "偏移表截断：需要到偏移 %d，实际长度 %d", tableEnd, len(data))
	}
	return header{count: count, groups: groups, tableEnd: tableEnd}, nil
}

// frameRange 返回第 g 组帧的字节区间 [start, end)，并校验帧连续、落在数据内。
func frameRange(data []byte, h header, g int) (int64, int64, error) {
	start := int64(binary.BigEndian.Uint64(data[headerSize+8*g:]))
	end := int64(len(data))
	if g+1 < int(h.groups) {
		end = int64(binary.BigEndian.Uint64(data[headerSize+8*(g+1):]))
	}
	if g == 0 && start != h.tableEnd {
		return 0, 0, decodeError(start, g, "组头缺失或错位：第 0 组应从偏移 %d 开始，实际为 %d", h.tableEnd, start)
	}
	if start < h.tableEnd || end < start+crcTrailerSize || end > int64(len(data)) {
		return 0, 0, decodeError(start, g, "组头缺失或帧越界：组 %d 区间 [%d,%d) 非法（数据长度 %d）", g, start, end, len(data))
	}
	return start, end, nil
}

// verifyAndDecodeGroup 校验第 g 组 CRC，并解出恰好 want 项。want 由头部项数推导。
func decodeGroup(data []byte, h header, g int, want int) ([]int64, error) {
	start, end, err := frameRange(data, h, g)
	if err != nil {
		return nil, err
	}
	payload := data[start : end-crcTrailerSize]
	stored := binary.BigEndian.Uint32(data[end-crcTrailerSize : end])
	if actual := crc32.ChecksumIEEE(payload); actual != stored {
		return nil, decodeError(start, g, "组 %d 校验和不匹配：期望 %08x，实际 %08x，数据已损坏", g, stored, actual)
	}

	out := make([]int64, 0, want)
	pos := 0
	read := func() (int64, error) {
		value, n, err := readVarint(payload[pos:], start+int64(pos))
		if err != nil {
			var de *DecodeError
			if errors.As(err, &de) {
				de.Group = g
			}
			return 0, err
		}
		pos += n
		return int64(value), nil
	}

	first, err := read()
	if err != nil {
		return nil, err
	}
	current := ZigZagDecode(uint64(first))
	out = append(out, current)
	for index := 1; index < want; index++ {
		raw, err := read()
		if err != nil {
			return nil, err
		}
		current += ZigZagDecode(uint64(raw))
		out = append(out, current)
	}
	if pos != len(payload) {
		return nil, decodeError(start+int64(pos), g, "组 %d 帧数与项数不符：解完 %d 项后偏移 %d 仍有多余字节（帧应到 %d）", g, want, start+int64(pos), end-int64(crcTrailerSize))
	}
	return out, nil
}

// Decode 单遍解码整个字节流并做全部校验。
func Decode(data []byte) ([]int64, error) {
	h, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	if h.count == 0 {
		return []int64{}, nil
	}

	out := make([]int64, 0, h.count)
	previousEnd := h.tableEnd
	for g := 0; g < int(h.groups); g++ {
		start, end, err := frameRange(data, h, g)
		if err != nil {
			return nil, err
		}
		if g > 0 && start != previousEnd {
			return nil, decodeError(start, g, "组头缺失或帧不连续：组 %d 应从偏移 %d 开始，实际为 %d", g, previousEnd, start)
		}
		want := GroupSize
		if remaining := int(h.count) - g*GroupSize; remaining < GroupSize {
			want = remaining
		}
		group, err := decodeGroup(data, h, g, want)
		if err != nil {
			return nil, err
		}
		out = append(out, group...)
		previousEnd = end
	}
	if previousEnd != int64(len(data)) {
		return nil, decodeError(previousEnd, int(h.groups)-1, "组数与实际不符：解完 %d 组后偏移 %d 处仍有多余字节（总长 %d）", h.groups, previousEnd, len(data))
	}
	if int64(len(out)) != h.count {
		return nil, decodeError(headerSize, -1, "头部声明项数 %d 与实际解出项数 %d 不一致", h.count, len(out))
	}
	return out, nil
}

// LookupAt 随机访问第 index 项（0 起）。只校验并解码它所在的那一组：
// O(1) 组定位 + O(组大小) 解码，内存 O(组大小)，绝不全量解码。
func LookupAt(data []byte, index int64) (int64, error) {
	h, err := parseHeader(data)
	if err != nil {
		return 0, err
	}
	if h.count == 0 {
		return 0, decodeError(-1, -1, "空数据没有可访问的项")
	}
	if index < 0 || index >= h.count {
		return 0, decodeError(-1, -1, "下标 %d 越界：合法范围 [0,%d)", index, h.count)
	}

	g := int(index / GroupSize)
	start, end, err := frameRange(data, h, g)
	if err != nil {
		return 0, err
	}
	payload := data[start : end-crcTrailerSize]
	stored := binary.BigEndian.Uint32(data[end-crcTrailerSize : end])
	if actual := crc32.ChecksumIEEE(payload); actual != stored {
		return 0, decodeError(start, g, "组 %d 校验和不匹配：期望 %08x，实际 %08x，数据已损坏", g, stored, actual)
	}

	position := int64(index % GroupSize)
	pos := 0
	readOne := func() (int64, error) {
		value, n, err := readVarint(payload[pos:], start+int64(pos))
		if err != nil {
			var de *DecodeError
			if errors.As(err, &de) {
				de.Group = g
			}
			return 0, err
		}
		pos += n
		return int64(value), nil
	}

	raw, err := readOne()
	if err != nil {
		return 0, err
	}
	current := ZigZagDecode(uint64(raw))
	for i := int64(1); i <= position; i++ {
		raw, err = readOne()
		if err != nil {
			return 0, err
		}
		current += ZigZagDecode(uint64(raw))
	}
	return current, nil
}
