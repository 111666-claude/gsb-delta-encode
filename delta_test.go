package deltaencode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"math/rand"
	"strings"
	"testing"
)

func TestZigzag(t *testing.T) {
	cases := []struct {
		value int64
		want  uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := zigzag(c.value); got != c.want {
			t.Errorf("zigzag(%d) = %d，want %d", c.value, got, c.want)
		}
		if got := unzigzag(c.want); got != c.value {
			t.Errorf("unzigzag(%d) = %d，want %d", c.want, got, c.value)
		}
	}
}

func TestReadUvarintBoundaries(t *testing.T) {
	for _, u := range []uint64{0, 1, 127, 128, 300, 1<<21 - 1, 1 << 21, 1<<63 - 1, 1 << 63, math.MaxUint64} {
		var buf [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(buf[:], u)
		got, off, err := readUvarint(buf[:n], 0, 0)
		if err != nil || got != u || off != n {
			t.Errorf("readUvarint(%d) = %d, %d, %v", u, got, off, err)
		}
	}
	if n := binary.PutUvarint(make([]byte, binary.MaxVarintLen64), math.MaxUint64); n != maxVarintLen {
		t.Fatalf("MaxUint64 应占 10 字节，实际 %d", n)
	}
}

func TestReadUvarintInvalid(t *testing.T) {
	if _, _, err := readUvarint([]byte{0x80}, 0, 0); err == nil ||
		!strings.Contains(err.Error(), "截断") {
		t.Errorf("截断的 varint 应报错：%v", err)
	}
	eleven := bytes.Repeat([]byte{0x80}, 11)
	if _, _, err := readUvarint(eleven, 0, 0); err == nil ||
		!strings.Contains(err.Error(), "超过 10 字节") {
		t.Errorf("超过 10 字节的 varint 应报错：%v", err)
	}
	overflow := append(bytes.Repeat([]byte{0x80}, 9), 0x02)
	if _, _, err := readUvarint(overflow, 0, 0); err == nil ||
		!strings.Contains(err.Error(), "溢出") {
		t.Errorf("第 10 字节溢出应报错：%v", err)
	}
	var dec *DecodeError
	if _, _, err := readUvarint([]byte{0x80}, 0, 0); !errors.As(err, &dec) || dec.Offset != 0 {
		t.Errorf("错误应带偏移：%v", err)
	}
}

// buildFrame 手工拼一帧数据，便于构造非法输入。
func buildFrame(itemCount, groupCount uint64, payloads [][]byte) []byte {
	body := []byte{}
	offsets := []uint64{}
	for _, payload := range payloads {
		offsets = append(offsets, uint64(len(body)))
		body = append(body, payload...)
		var sum [checksumSize]byte
		binary.BigEndian.PutUint32(sum[:], crc32.ChecksumIEEE(payload))
		body = append(body, sum[:]...)
	}
	header := binary.AppendUvarint(nil, itemCount)
	header = binary.AppendUvarint(header, groupCount)
	base := uint64(len(header) + len(offsets)*offsetSize)
	var tmp [offsetSize]byte
	for _, off := range offsets {
		binary.BigEndian.PutUint64(tmp[:], base+off)
		header = append(header, tmp[:]...)
	}
	return append(header, body...)
}

func payloadOf(first int64, deltas ...int64) []byte {
	out := binary.AppendUvarint(nil, zigzag(first))
	for _, d := range deltas {
		out = binary.AppendUvarint(out, zigzag(d))
	}
	return out
}

func TestFramingGroupCount(t *testing.T) {
	values := make([]int64, 300)
	for i := range values {
		values[i] = int64(i)
	}
	encoded := Encode(values)
	itemCount, n := binary.Uvarint(encoded)
	groupCount, _ := binary.Uvarint(encoded[n:])
	if itemCount != 300 || groupCount != 3 {
		t.Fatalf("头部应为 300 项 3 组，实际 %d 项 %d 组", itemCount, groupCount)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for i := range values {
		if decoded[i] != values[i] {
			t.Fatalf("第 %d 项不一致", i)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	cases := map[string][]int64{
		"空":    {},
		"单项":   {42},
		"负数":   {-1, -2, -3, -100},
		"递减":   {5, 4, 3, 2, 1},
		"重复":   {7, 7, 7, 7},
		"大跨度":  {math.MinInt64 / 2, math.MaxInt64 / 2, -1000, 0, math.MaxInt64 - 1},
		"极值":   {math.MinInt64, -1},
		"极值单项": {math.MaxInt64},
		"跨组":   nil,
		"随机":   nil,
	}
	cases["跨组"] = make([]int64, GroupSize*2+3)
	for i := range cases["跨组"] {
		cases["跨组"][i] = int64(i * i)
	}
	rng := rand.New(rand.NewSource(1))
	cases["随机"] = make([]int64, 1000)
	for i := range cases["随机"] {
		cases["随机"][i] = rng.Int63n(2000) - 1000
	}
	for name, values := range cases {
		encoded := Encode(values)
		decoded, err := Decode(encoded)
		if err != nil {
			t.Fatalf("%s：Decode 出错：%v", name, err)
		}
		if len(decoded) != len(values) {
			t.Fatalf("%s：长度 %d != %d", name, len(decoded), len(values))
		}
		for i := range values {
			if decoded[i] != values[i] {
				t.Fatalf("%s：第 %d 项 %d != %d", name, i, decoded[i], values[i])
			}
		}
	}
}

func TestDeterministic(t *testing.T) {
	values := []int64{3, -1, 4, -1, 5, -9, 2, 6}
	if !bytes.Equal(Encode(values), Encode(values)) {
		t.Fatal("同一输入两次编码应逐字节相同")
	}
}

func TestEmptyInput(t *testing.T) {
	if enc := Encode(nil); len(enc) != 0 {
		t.Fatalf("空输入的编码应为空：%v", enc)
	}
	decoded, err := Decode(nil)
	if err != nil || len(decoded) != 0 {
		t.Fatalf("空数据应解出空：%v %v", decoded, err)
	}
}

func TestSizePerItem(t *testing.T) {
	values := make([]int64, 10000)
	for i := range values {
		values[i] = int64(i)
	}
	encoded := Encode(values)
	if per := float64(len(encoded)) / float64(len(values)); per >= 2 {
		t.Fatalf("小差分序列每项应少于 2 字节，实际 %.2f", per)
	}
}

func TestAt(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	values := make([]int64, 500)
	current := int64(-100)
	for i := range values {
		current += rng.Int63n(21) - 10
		values[i] = current
	}
	encoded := Encode(values)
	for _, index := range []uint64{0, 1, 127, 128, 129, 255, 499} {
		got, err := At(encoded, index)
		if err != nil {
			t.Fatalf("At(%d) 出错：%v", index, err)
		}
		if got != values[index] {
			t.Fatalf("At(%d) = %d，want %d", index, got, values[index])
		}
	}
	if _, err := At(encoded, 500); err == nil {
		t.Fatal("越界下标应报错")
	}
	if _, err := At(nil, 0); err == nil {
		t.Fatal("空数据应报错")
	}
}

// groupOffset 从头部读第 g 组的绝对偏移，测试用。
func groupOffset(t *testing.T, data []byte, g int) int {
	t.Helper()
	_, n := binary.Uvarint(data)
	_, m := binary.Uvarint(data[n:])
	return int(binary.BigEndian.Uint64(data[n+m+g*offsetSize:]))
}

func TestChecksumDetectsCorruption(t *testing.T) {
	values := make([]int64, 300)
	for i := range values {
		values[i] = int64(i * 3)
	}
	encoded := Encode(values)
	at := groupOffset(t, encoded, 1)
	encoded[at+2] ^= 0x01 // 改动第 1 组载荷的一位
	_, err := Decode(encoded)
	var sumErr *ChecksumError
	if !errors.As(err, &sumErr) {
		t.Fatalf("应报校验和错误：%v", err)
	}
	if sumErr.Group != 1 {
		t.Fatalf("应指出第 1 组，实际第 %d 组", sumErr.Group)
	}
}

func TestChecksumCorruptsChecksumBytes(t *testing.T) {
	values := []int64{10, 20, 30}
	encoded := Encode(values)
	encoded[len(encoded)-1] ^= 0xff // 改动校验和本身
	if _, err := Decode(encoded); err == nil {
		t.Fatal("校验和被改动应报错")
	}
}

func TestAtUnaffectedByOtherGroupCorruption(t *testing.T) {
	values := make([]int64, 300)
	for i := range values {
		values[i] = int64(i)
	}
	encoded := Encode(values)
	at := groupOffset(t, encoded, 2)
	encoded[at] ^= 0x01        // 只改第 2 组
	got, err := At(encoded, 5) // 第 0 组
	if err != nil || got != 5 {
		t.Fatalf("其它组损坏不该影响本组随机访问：%d %v", got, err)
	}
	if _, err := At(encoded, 260); err == nil { // 第 2 组
		t.Fatal("本组损坏应报错")
	}
}

func TestDecodeInvalidInputs(t *testing.T) {
	t.Run("varint 截断", func(t *testing.T) {
		// 首值 varint 声明有后续字节但被截断
		payload := []byte{0x80}
		data := buildFrame(2, 1, [][]byte{payload})
		var dec *DecodeError
		if _, err := Decode(data); !errors.As(err, &dec) || !strings.Contains(err.Error(), "截断") {
			t.Fatalf("应报 varint 截断：%v", err)
		}
	})
	t.Run("varint 超过 10 字节", func(t *testing.T) {
		payload := bytes.Repeat([]byte{0x80}, 11)
		data := buildFrame(1, 1, [][]byte{payload})
		if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "超过 10 字节") {
			t.Fatalf("应报 varint 超宽：%v", err)
		}
	})
	t.Run("累积溢出", func(t *testing.T) {
		payload := payloadOf(math.MaxInt64, 1)
		data := buildFrame(2, 1, [][]byte{payload})
		var dec *DecodeError
		if _, err := Decode(data); !errors.As(err, &dec) || !strings.Contains(err.Error(), "溢出") {
			t.Fatalf("应报累积溢出：%v", err)
		}
	})
	t.Run("组数与实际不符", func(t *testing.T) {
		data := buildFrame(5, 2, [][]byte{payloadOf(1, 1, 1, 1, 1)})
		if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "组数与实际不符") {
			t.Fatalf("应报组数不符：%v", err)
		}
	})
	t.Run("组头缺失", func(t *testing.T) {
		// 组范围只有校验和、没有载荷
		itemCount, groupCount := uint64(1), uint64(1)
		header := binary.AppendUvarint(nil, itemCount)
		header = binary.AppendUvarint(header, groupCount)
		base := uint64(len(header) + offsetSize)
		var tmp [offsetSize]byte
		binary.BigEndian.PutUint64(tmp[:], base)
		data := append(header, tmp[:]...)
		data = append(data, 0, 0, 0, 0) // 只有 4 字节校验和
		if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "非法") {
			t.Fatalf("应报组范围非法：%v", err)
		}
	})
	t.Run("项数与头部不符", func(t *testing.T) {
		data := buildFrame(3, 1, [][]byte{payloadOf(1, 1)}) // 声明 3 项只有 2 项
		if _, err := Decode(data); err == nil {
			t.Fatal("项数不符应报错")
		}
	})
	t.Run("头部截断", func(t *testing.T) {
		if _, err := Decode([]byte{0x85}); err == nil {
			t.Fatal("头部 varint 截断应报错")
		}
	})
	t.Run("偏移表截断", func(t *testing.T) {
		data := binary.AppendUvarint(nil, 1)
		data = binary.AppendUvarint(data, 1)
		data = append(data, 0, 0) // 偏移表不足 8 字节
		if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "偏移表截断") {
			t.Fatalf("应报偏移表截断：%v", err)
		}
	})
	t.Run("组内多出字节", func(t *testing.T) {
		payload := append(payloadOf(1, 1), 0x02)
		data := buildFrame(2, 1, [][]byte{payload})
		if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "多出") {
			t.Fatalf("应报组内多出字节：%v", err)
		}
	})
	t.Run("错误带偏移", func(t *testing.T) {
		payload := append(payloadOf(5), 0x80) // 第二个 varint 截断
		data := buildFrame(2, 1, [][]byte{payload})
		var dec *DecodeError
		if !errors.As(Decode2(data), &dec) || dec.Offset <= 0 {
			t.Fatalf("错误应带非零偏移：%v", dec)
		}
	})
}

// Decode2 只是为了让 errors.As 的写法更短的辅助。
func Decode2(data []byte) error {
	_, err := Decode(data)
	return err
}
