# delta-encode

把整数序列按 128 项分帧，帧内差分 + zigzag + varint（LEB128）编码，只用 Go 标准库
（`encoding/binary`、`hash/crc32`）。

```
go test ./...
go vet ./...
go run ./cmd/delta-encode --generate 10000
go run ./cmd/delta-encode --values "5,4,3,2,1" --roundtrip
go run ./cmd/delta-encode --generate 10000 --at 9999
```

## 格式

- **头部**：项数 uvarint、组数 uvarint、每组一个 8 字节大端绝对偏移（指向组载荷）。
- **每组（128 项，末组可不足）**：首值存绝对值（zigzag + varint），其余只存差分
  （zigzag + varint），末尾 4 字节 CRC32（组内载荷）。
- 空输入编成空（无头部）；同一输入编码逐字节确定。

## 口径

- **差分后 zigzag + varint**：小差分只占 1 字节。`--generate 10000` 约 1.1 字节/项
  （含帧头与校验和开销）；`--values "5,4,3,2,1"` 的差分载荷每项 1 字节，
  小输入的总字节由帧头（项数/组数/偏移表）与每组 CRC32 主导。
- **负数差分能还原**：zigzag 把有符号差分映射成无符号再 varint。
- **随机访问**：`--at N`（0 起）按偏移表 O(1) 定位所在组，只解码该组，
  时间 O(组大小)、内存 O(组大小)，不做全量解码。
- **分组校验和**：每组带 CRC32，数据被改动一位即报错并指出是哪一组。
- **解码校验**：varint 截断、超过 10 字节、累积溢出、组头缺失、组数与实际不符
  都报错并给出偏移。
- **不变量**：`Decode(Encode(values)) == values`（含递减、重复、大跨度、含负数）；
  空输入编成空、解出空；头部声明的项数与实际解出的项数一致。
- **规模**：编码内存 O(输出字节)，解码单遍 O(字节数)；不写每项固定 8 字节。

## 输出

```
count=… bytes=… bytes_per_item=…
roundtrip=true|false      （仅 --roundtrip）
at[N]=…                   （仅 --at N）
```

## 目录

```
delta.go          编解码（分帧、zigzag+varint、CRC32、随机访问）
cmd/delta-encode  命令行入口
delta_test.go     go test 用例
```
