# delta-encode

把整数序列编成差分 + 变长字节，只用 Go 标准库。

```
go test ./...
go run ./cmd/delta-encode --generate 10000
go run ./cmd/delta-encode --values "5,4,3,2,1" --roundtrip
```

## 口径（README 为准）

- **差分后用 zigzag + varint（LEB128）编码**：小差分只占 1 字节。
  `--generate 10000`（递增、平均差分很小）现在 `bytes=80000`（每项固定 8 字节），
  按口径应该每项 1–2 字节；`--values "5,4,3,2,1"` 现在 40 字节，按口径应该 5–7 字节。
- **负数差分要能还原**：zigzag 把有符号差分映射成无符号再 varint。
- 解码要校验：varint 截断、超过 10 字节的非法 varint、累积溢出都要报错并给出偏移。
- 不变量：`Decode(Encode(values)) == values`（含递减、重复、大跨度、含负数）；空输入编成空、解出空；
  同一输入重复编码结果逐字节相同。
- 规模：100 万项的编码内存 O(输出字节)，解码单遍 O(字节数)；不许每项都写 8 字节。

## 现在的行为

- `Encode` 对每个差分 `binary.BigEndian.PutUint64` 写满 8 字节：10000 项给 80000 字节。
- `Decode` 按固定 8 字节步长读，长度不是 8 的倍数就报错。
- 没有 zigzag、没有 varint，压缩率与数据分布无关。

## 计划新增的能力

```
--values 列表    逗号分隔的整数
--roundtrip      编码后解码并比对
```

## 输出

```
count=… bytes=… bytes_per_item=…
roundtrip=true|false
```

## 目录

```
delta.go          编解码
cmd/delta-encode  命令行入口
delta_test.go     go test 用例
```
