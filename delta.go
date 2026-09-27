// Package deltaencode 把整数序列按 128 项分帧，帧内差分 + zigzag + varint 编码。
//
// 格式（空输入编成空，无头部）：
//
//	头部：项数 uvarint、组数 uvarint、每组一个 8 字节大端绝对偏移（指向组载荷）
//	每组：首值绝对值 zigzag+varint，随后组内差分 zigzag+varint，末尾 4 字节 CRC32(载荷)
package deltaencode

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
)

// GroupSize 是每帧（组）的项数。
const GroupSize = 128

const (
	checksumSize = 4
	offsetSize   = 8
	maxVarintLen = 10
)

// DecodeError 描述解码失败，Offset 是出错字节在输入中的偏移。
type DecodeError struct {
	Offset int
	Msg    string
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("解码失败（偏移 %d）：%s", e.Offset, e.Msg)
}

// ChecksumError 表示某一组的 CRC32 校验和不对，数据可能被改动。
type ChecksumError struct {
	Group int
}

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("第 %d 组校验和不匹配，数据可能被改动", e.Group)
}

func zigzag(value int64) uint64 {
	return uint64(value<<1) ^ uint64(value>>63)
}

func unzigzag(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// readUvarint 从 data[off] 读一个 LEB128 varint，报错时偏移按 base 折算成绝对偏移。
func readUvarint(data []byte, off, base int) (uint64, int, error) {
	var value uint64
	for i := 0; ; i++ {
		if off+i >= len(data) {
			return 0, 0, &DecodeError{Offset: base + off, Msg: "varint 截断"}
		}
		b := data[off+i]
		if i == maxVarintLen-1 {
			if b&0x80 != 0 {
				return 0, 0, &DecodeError{Offset: base + off, Msg: "varint 超过 10 字节"}
			}
			if b > 1 {
				return 0, 0, &DecodeError{Offset: base + off + i, Msg: "varint 第 10 字节溢出 64 位"}
			}
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return value, off + i + 1, nil
		}
	}
}

// Encode 编码 values。空输入编成空；同一输入输出逐字节确定。
func Encode(values []int64) []byte {
	if len(values) == 0 {
		return nil
	}
	groupCount := (len(values) + GroupSize - 1) / GroupSize
	body := make([]byte, 0, len(values)+groupCount*(checksumSize+2))
	offsets := make([]uint64, 0, groupCount)
	for start := 0; start < len(values); start += GroupSize {
		end := start + GroupSize
		if end > len(values) {
			end = len(values)
		}
		offsets = append(offsets, uint64(len(body)))
		payloadStart := len(body)
		body = binary.AppendUvarint(body, zigzag(values[start]))
		previous := values[start]
		for _, value := range values[start+1 : end] {
			body = binary.AppendUvarint(body, zigzag(value-previous))
			previous = value
		}
		var sum [checksumSize]byte
		binary.BigEndian.PutUint32(sum[:], crc32.ChecksumIEEE(body[payloadStart:]))
		body = append(body, sum[:]...)
	}
	header := make([]byte, 0, 2*binary.MaxVarintLen64+groupCount*offsetSize)
	header = binary.AppendUvarint(header, uint64(len(values)))
	header = binary.AppendUvarint(header, uint64(groupCount))
	base := uint64(len(header) + groupCount*offsetSize)
	var tmp [offsetSize]byte
	for _, off := range offsets {
		binary.BigEndian.PutUint64(tmp[:], base+off)
		header = append(header, tmp[:]...)
	}
	return append(header, body...)
}

// parseHeader 读头部并校验组数与项数一致，返回偏移表起点。
func parseHeader(data []byte) (itemCount, groupCount uint64, indexStart int, err error) {
	itemCount, off, err := readUvarint(data, 0, 0)
	if err != nil {
		return 0, 0, 0, err
	}
	groupCount, indexStart, err = readUvarint(data, off, 0)
	if err != nil {
		return 0, 0, 0, err
	}
	if itemCount == 0 {
		return 0, 0, 0, &DecodeError{Offset: 0, Msg: "头部为空但总数据非空"}
	}
	expected := (itemCount-1)/GroupSize + 1
	if groupCount != expected {
		return 0, 0, 0, &DecodeError{
			Offset: off,
			Msg:    fmt.Sprintf("组数与实际不符：头部声明 %d 组，按 %d 项应为 %d 组", groupCount, itemCount, expected),
		}
	}
	if uint64(len(data)-indexStart) < groupCount*offsetSize {
		return 0, 0, 0, &DecodeError{Offset: len(data), Msg: "组偏移表截断"}
	}
	return itemCount, groupCount, indexStart, nil
}

// groupBounds 返回第 g 组 [start, end) 的绝对范围（含校验和）。
func groupBounds(data []byte, indexStart int, groupCount, g uint64) (uint64, uint64, error) {
	bodyStart := uint64(indexStart) + groupCount*offsetSize
	at := indexStart + int(g)*offsetSize
	start := binary.BigEndian.Uint64(data[at : at+offsetSize])
	end := uint64(len(data))
	if g+1 < groupCount {
		end = binary.BigEndian.Uint64(data[at+offsetSize : at+2*offsetSize])
	}
	if start < bodyStart || start >= uint64(len(data)) {
		return 0, 0, &DecodeError{Offset: at, Msg: fmt.Sprintf("第 %d 组偏移 %d 越界", g, start)}
	}
	if end < start+checksumSize+1 || end > uint64(len(data)) {
		return 0, 0, &DecodeError{Offset: at, Msg: fmt.Sprintf("第 %d 组范围 [%d,%d) 非法", g, start, end)}
	}
	return start, end, nil
}

// verifiedPayload 校验第 g 组的 CRC32 并返回载荷。
func verifiedPayload(data []byte, start, end uint64, g uint64) ([]byte, error) {
	payload := data[start : end-checksumSize]
	want := binary.BigEndian.Uint32(data[end-checksumSize : end])
	if crc32.ChecksumIEEE(payload) != want {
		return nil, &ChecksumError{Group: int(g)}
	}
	return payload, nil
}

func groupLen(itemCount, g uint64) uint64 {
	remaining := itemCount - g*GroupSize
	if remaining > GroupSize {
		return GroupSize
	}
	return remaining
}

// decodeGroup 解码一组载荷，base 是载荷在原始输入中的绝对偏移。
func decodeGroup(payload []byte, base int, count uint64) ([]int64, error) {
	if len(payload) == 0 {
		return nil, &DecodeError{Offset: base, Msg: "组头缺失：组内没有首值"}
	}
	out := make([]int64, 0, count)
	u, off, err := readUvarint(payload, 0, base)
	if err != nil {
		return nil, err
	}
	previous := unzigzag(u)
	out = append(out, previous)
	for i := uint64(1); i < count; i++ {
		u, next, err := readUvarint(payload, off, base)
		if err != nil {
			return nil, err
		}
		delta := unzigzag(u)
		if (delta > 0 && previous > math.MaxInt64-delta) || (delta < 0 && previous < math.MinInt64-delta) {
			return nil, &DecodeError{Offset: base + off, Msg: "累积溢出"}
		}
		previous += delta
		out = append(out, previous)
		off = next
	}
	if off != len(payload) {
		return nil, &DecodeError{Offset: base + off, Msg: fmt.Sprintf("组内多出 %d 字节", len(payload)-off)}
	}
	return out, nil
}

// Decode 解码。校验 varint、溢出、组头、组数、校验和与项数。
func Decode(data []byte) ([]int64, error) {
	if len(data) == 0 {
		return []int64{}, nil
	}
	itemCount, groupCount, indexStart, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	capacity := itemCount
	if capacity > uint64(len(data)) {
		capacity = uint64(len(data))
	}
	out := make([]int64, 0, capacity)
	for g := uint64(0); g < groupCount; g++ {
		start, end, err := groupBounds(data, indexStart, groupCount, g)
		if err != nil {
			return nil, err
		}
		payload, err := verifiedPayload(data, start, end, g)
		if err != nil {
			return nil, err
		}
		values, err := decodeGroup(payload, int(start), groupLen(itemCount, g))
		if err != nil {
			return nil, err
		}
		out = append(out, values...)
	}
	if uint64(len(out)) != itemCount {
		return nil, &DecodeError{
			Offset: len(data),
			Msg:    fmt.Sprintf("头部声明 %d 项，实际解出 %d 项", itemCount, len(out)),
		}
	}
	return out, nil
}

// At 随机访问第 index 项（0 起）：O(1) 定位所在组，只解码该组，内存 O(组大小)。
func At(data []byte, index uint64) (int64, error) {
	if len(data) == 0 {
		return 0, &DecodeError{Offset: 0, Msg: "空数据"}
	}
	itemCount, groupCount, indexStart, err := parseHeader(data)
	if err != nil {
		return 0, err
	}
	if index >= itemCount {
		return 0, &DecodeError{Offset: 0, Msg: fmt.Sprintf("下标 %d 越界（共 %d 项）", index, itemCount)}
	}
	g := index / GroupSize
	start, end, err := groupBounds(data, indexStart, groupCount, g)
	if err != nil {
		return 0, err
	}
	payload, err := verifiedPayload(data, start, end, g)
	if err != nil {
		return 0, err
	}
	values, err := decodeGroup(payload, int(start), groupLen(itemCount, g))
	if err != nil {
		return 0, err
	}
	return values[index%GroupSize], nil
}
