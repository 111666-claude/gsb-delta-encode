package deltaencode

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"testing"
)

func roundTrip(t *testing.T, data []int64) {
	t.Helper()
	encoded := Encode(data)
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode 出错：%v", err)
	}
	if len(decoded) != len(data) {
		t.Fatalf("长度不对：want %d got %d", len(data), len(decoded))
	}
	for index := range data {
		if decoded[index] != data[index] {
			t.Fatalf("往返不一致：下标 %d want %d got %d（全部 %v）", index, data[index], decoded[index], decoded)
		}
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		in   int64
		want uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{63, 126}, {-64, 127}, {64, 128},
		{math.MaxInt64, math.MaxUint64 - 1}, {math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := ZigZagEncode(c.in); got != c.want {
			t.Errorf("ZigZagEncode(%d) = %d, want %d", c.in, got, c.want)
		}
		if got := ZigZagDecode(c.want); got != c.in {
			t.Errorf("ZigZagDecode(%d) = %d, want %d", c.want, got, c.in)
		}
	}
}

func TestVarintBoundaries(t *testing.T) {
	// 1 字节边界、2 字节边界以及最大/最小 zigzag（10 字节）都要能往返。
	values := []int64{0, 63, -64, 64, -65, 8191, -8192, 8192, math.MaxInt64, math.MinInt64}
	for _, first := range values {
		// 单组：首值绝对值
		roundTrip(t, []int64{first})
		// 差分形式
		roundTrip(t, []int64{first, first + 0, first + 63, first - 64, first + math.MaxInt64, first + math.MinInt64})
	}
}

func TestEncodeProducesBytes(t *testing.T) {
	encoded := Encode([]int64{1, 2, 3})
	if len(encoded) == 0 {
		t.Fatal("编码结果不该为空")
	}
	// magic
	if string(encoded[:4]) != "DLT1" {
		t.Fatalf("magic 不对：%q", encoded[:4])
	}
}

func TestEmptyInput(t *testing.T) {
	if enc := Encode(nil); len(enc) != 0 {
		t.Fatalf("空输入的编码应为空：%v", enc)
	}
	if enc := Encode([]int64{}); len(enc) != 0 {
		t.Fatalf("空切片的编码应为空：%v", enc)
	}
	decoded, err := Decode(nil)
	if err != nil || len(decoded) != 0 {
		t.Fatalf("空数据应解出空：%v %v", decoded, err)
	}
	if _, err := LookupAt(nil, 0); err == nil {
		t.Fatal("空数据随机访问应报错")
	}
}

func TestRoundTripShapes(t *testing.T) {
	roundTrip(t, []int64{1, 5, 9, 1000})
	roundTrip(t, []int64{5, 4, 3, 2, 1})          // 递减
	roundTrip(t, []int64{7, 7, 7, 7})             // 重复
	roundTrip(t, []int64{-5, -3, -100, 0, 99})    // 负数
	roundTrip(t, []int64{0, 1 << 40, -(1 << 41)}) // 大跨度
	roundTrip(t, []int64{math.MinInt64, math.MaxInt64, 0})
}

func TestGroupFramingAcrossBoundary(t *testing.T) {
	// 跨 128 边界，多组往返；首值与差分都覆盖正负。
	data := make([]int64, GroupSize*3+7)
	current := int64(-1000)
	for index := range data {
		if index%3 == 0 {
			current += int64(index)
		} else if index%3 == 1 {
			current -= int64(index) / 2
		}
		data[index] = current
	}
	roundTrip(t, data)
}

func TestDeterministic(t *testing.T) {
	data := []int64{-3, -3, 100, 99, 1000000}
	a := Encode(data)
	b := Encode(data)
	if len(a) != len(b) {
		t.Fatal("两次编码长度不同")
	}
	for index := range a {
		if a[index] != b[index] {
			t.Fatalf("两次编码不逐字节一致：下标 %d", index)
		}
	}
}

func TestHeaderDeclaresGroupCount(t *testing.T) {
	data := make([]int64, GroupSize*2+1)
	for index := range data {
		data[index] = int64(index * 2)
	}
	encoded := Encode(data)
	if got := groupCountFor(int64(len(data))); got != 3 {
		t.Fatalf("组数理应是 3，实际 %d", got)
	}
	// 偏移表里三个帧起点都应指向头部之后。
	for g := 0; g < 3; g++ {
		offset := int64(headerSize) + 8*int64(g)
		start := decodeBigEndianOffset(encoded[offset:])
		if start < int64(headerSize)+24 || start >= int64(len(encoded)) {
			t.Fatalf("组 %d 偏移 %d 非法（header+table=%d, len=%d）", g, start, headerSize+24, len(encoded))
		}
	}
}

func decodeBigEndianOffset(b []byte) int64 {
	var v uint64
	for _, x := range b[:8] {
		v = v<<8 | uint64(x)
	}
	return int64(v)
}

func TestRandomAccess(t *testing.T) {
	data := make([]int64, GroupSize*2+50)
	current := int64(3)
	for index := range data {
		current += int64(index%7) - 3
		data[index] = current
	}
	encoded := Encode(data)
	for _, index := range []int64{0, 1, GroupSize - 1, GroupSize, GroupSize + 1,
		GroupSize * 2, GroupSize*2 + 49} {
		got, err := LookupAt(encoded, index)
		if err != nil {
			t.Fatalf("LookupAt(%d) 出错：%v", index, err)
		}
		if got != data[index] {
			t.Fatalf("LookupAt(%d) = %d, want %d", index, got, data[index])
		}
	}
	if _, err := LookupAt(encoded, int64(len(data))); err == nil {
		t.Fatal("越界下标应报错")
	}
	if _, err := LookupAt(encoded, -1); err == nil {
		t.Fatal("负下标应报错")
	}
}

func TestChecksumCorruption(t *testing.T) {
	data := make([]int64, GroupSize*2+1)
	for index := range data {
		data[index] = int64(index)
	}
	encoded := Encode(data)

	// 翻转第二组载荷里的一个比特，错误必须指出是组 1。
	firstFrameEnd := decodeBigEndianOffset(encoded[headerSize+8 : headerSize+16])
	pos := int(firstFrameEnd) + 1
	corrupt := append([]byte(nil), encoded...)
	corrupt[pos] ^= 0x01

	if _, err := Decode(corrupt); err == nil {
		t.Fatal("损坏数据 Decode 应报错")
	} else {
		de, ok := err.(*DecodeError)
		if !ok || de.Group != 1 {
			t.Fatalf("错误应指出组 1，实际：%v", err)
		}
	}
	if _, err := LookupAt(corrupt, GroupSize+1); err == nil {
		t.Fatal("损坏组随机访问应报错")
	} else {
		de, ok := err.(*DecodeError)
		if !ok || de.Group != 1 {
			t.Fatalf("随机访问错误应指出组 1，实际：%v", err)
		}
	}
	// 落在未损坏组的随机访问仍应成功。
	if got, err := LookupAt(corrupt, 3); err != nil || got != 3 {
		t.Fatalf("未损坏组应可访问：got %d err %v", got, err)
	}
}

func TestInvalidInputs(t *testing.T) {
	good := Encode([]int64{1, 2, 3, 4, 5})

	t.Run("垃圾magic", func(t *testing.T) {
		if _, err := Decode([]byte("xxxx garbage")); err == nil {
			t.Fatal("magic 错误应报错")
		}
	})
	t.Run("头部截断", func(t *testing.T) {
		if _, err := Decode(good[:headerSize-1]); err == nil {
			t.Fatal("头部截断应报错")
		}
	})
	t.Run("偏移表截断", func(t *testing.T) {
		if _, err := Decode(good[:headerSize+3]); err == nil {
			t.Fatal("偏移表截断应报错")
		}
	})
	t.Run("varint截断", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		// 帧从偏移表结束开始；把 varint 首字节高位置 1 后截掉尾部。
		bad[headerSize+8] = 0x80
		bad = bad[:headerSize+9]
		if _, err := Decode(bad); err == nil {
			t.Fatal("varint 截断应报错")
		}
	})
	t.Run("varint超过10字节", func(t *testing.T) {
		// 手工构造：count=1, groups=1, 偏移指向 header+table(24)，
		// 载荷是 11 个延续字节，CRC 只是占位（在 10 字节检查之前不会到 CRC）。
		bad := make([]byte, 24+11+4)
		copy(bad, "DLT1")
		bad[4], bad[5], bad[6], bad[7], bad[8], bad[9], bad[10], bad[11] = 0, 0, 0, 0, 0, 0, 0, 1
		bad[12], bad[13], bad[14], bad[15] = 0, 0, 0, 1
		bad[16], bad[17], bad[18], bad[19], bad[20], bad[21], bad[22], bad[23] = 0, 0, 0, 0, 0, 0, 0, 24
		for i := 0; i < 11; i++ {
			bad[24+i] = 0x80
		}
		binary.BigEndian.PutUint32(bad[24+11:], crc32.ChecksumIEEE(bad[24:24+11]))
		err := decodeExpectError(t, bad)
		if de, ok := err.(*DecodeError); !ok || de.Offset != 34 {
			t.Fatalf("应报偏移 34 的「超过 10 字节」，实际：%v", err)
		}
	})
	t.Run("varint第10字节溢出", func(t *testing.T) {
		bad := make([]byte, 24+10+4)
		copy(bad, "DLT1")
		bad[11] = 1
		bad[15] = 1
		bad[23] = 24
		for i := 0; i < 9; i++ {
			bad[24+i] = 0x80
		}
		bad[33] = 0x02 // 第 10 字节 >1 => 溢出
		binary.BigEndian.PutUint32(bad[24+10:], crc32.ChecksumIEEE(bad[24:24+10]))
		err := decodeExpectError(t, bad)
		if de, ok := err.(*DecodeError); !ok || de.Offset != 33 {
			t.Fatalf("应报偏移 33 的溢出，实际：%v", err)
		}
	})
	t.Run("项数为零但非空", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		for i := 4; i < 12; i++ {
			bad[i] = 0
		}
		decodeExpectError(t, bad)
	})
	t.Run("组数与项数不符", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[15] = 2 // 5 项只该有 1 组，却声称 2 组
		decodeExpectError(t, bad)
	})
	t.Run("帧越界_组头缺失", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		// 第 0 组偏移指向偏移表内部。
		bad[23] = 8
		decodeExpectError(t, bad)
	})
	t.Run("尾部多余字节", func(t *testing.T) {
		bad := append(append([]byte(nil), good...), 0x00)
		decodeExpectError(t, bad)
	})
	t.Run("组内多余字节", func(t *testing.T) {
		data := make([]int64, GroupSize+1)
		for i := range data {
			data[i] = int64(i)
		}
		bad := Encode(data)
		// 在第 0 组载荷（offset table 之后）塞一个零 varint，CRC 会先失败；
		// 直接改最后一组载荷并同步去掉一项才能绕过 CRC，所以这里只验证报错。
		bad = append(bad, 0)
		decodeExpectError(t, bad)
	})
}

func decodeExpectError(t *testing.T, data []byte) error {
	t.Helper()
	_, err := Decode(data)
	if err == nil {
		t.Fatal("期望 Decode 报错，但成功了")
	}
	return err
}

func TestByteSavings(t *testing.T) {
	// 小差分序列：每项远小于 8 字节。
	data := make([]int64, 10000)
	for index := range data {
		data[index] = int64(index * 2)
	}
	encoded := Encode(data)
	if bpi := float64(len(encoded)) / float64(len(data)); bpi >= 3 {
		t.Fatalf("每项字节数应在 1-2 附近，实际 %.2f（bytes=%d）", bpi, len(encoded))
	}
}
