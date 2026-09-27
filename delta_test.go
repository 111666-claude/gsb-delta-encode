package deltaencode

import "testing"

func TestEncodeProducesBytes(t *testing.T) {
encoded := Encode([]int64{1, 2, 3})
if len(encoded) == 0 {
t.Fatal("编码结果不该为空")
}
}

func TestDecodeRejectsBadLength(t *testing.T) {
if _, err := Decode([]byte{1, 2, 3}); err == nil {
t.Fatal("长度不是 8 的倍数应该报错")
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

func TestRoundTripIncreasing(t *testing.T) {
data := []int64{1, 5, 9, 1000}
decoded, err := Decode(Encode(data))
if err != nil {
t.Fatalf("Decode 出错：%v", err)
}
if len(decoded) != len(data) {
t.Fatalf("长度不对：%v", decoded)
}
for index := range data {
if decoded[index] != data[index] {
t.Fatalf("往返不一致：%v", decoded)
}
}
}
