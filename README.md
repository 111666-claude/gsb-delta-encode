# delta-encode

把整数序列编成「分帧 + 差分 + zigzag/varint」字节流，只用 Go 标准库
（`encoding/binary`、`hash/crc32`）。

```
go test ./...
go vet ./...
go run ./cmd/delta-encode --generate 10000
go run ./cmd/delta-encode --values "5,4,3,2,1" --roundtrip --at 2
```

## 线格式

多字节整数均为大端；空输入编成空字节流。

```
头部  magic "DLT1"(4) | count uint64(8) | groupCount uint32(4)
      | groupCount 个组帧绝对偏移 uint64
组帧  组内首值绝对值(zigzag + LEB128)
      后跟 (组大小-1) 个差分(zigzag + LEB128)
      末尾 CRC32-IEEE(4)，覆盖组头与全部差分
```

- **zigzag + varint**：差分先 zigzag 映射成无符号（`x<<1 ^ x>>63`）再写
  LEB128，小差分（含负差分）只占 1 字节；`--generate 10000` 实测
  `bytes_per_item≈1.11`（旧版固定 8 字节）。
- **分帧**：每 128 项一组（`GroupSize`），组头存该组第一个值的绝对值，
  组内只存与前一项的差分；组数写进头部，头部还带每组的帧偏移表。
- **随机访问**：`--at N`（0 起下标）经偏移表 O(1) 定位组帧，只校验、
  解码那一组，复杂度 O(组大小)、内存 O(组大小)，绝不全量解码。
- **分组校验和**：每组末尾一个 CRC32；数据被改动一位即报错，并指出是
  第几组（错误信息带组号与字节偏移）。

## 解码校验

以下情况都返回带字节偏移（及组号）的 `DecodeError`：

- varint 截断（延续位为 1 但数据结束）
- varint 超过 10 字节
- varint 累积溢出（第 10 字节数据位超过 1）
- 组头缺失、帧不连续或越界
- 头部声明的组数/项数与实际不符、尾部或组内有多余字节
- 组 CRC32 不匹配
- magic 不匹配、头部或偏移表截断、空数据上做随机访问或下标越界

## 不变量

- `Decode(Encode(values)) == values`：负数、递减、重复、大跨度
  （`MinInt64/MaxInt64` 级）序列均可往返。
- 同一输入 `Encode` 结果逐字节相同；空输入编成空、解出空。
- 头部声明的项数与实际解出的项数一致。

## 规模

- 编码额外内存 O(输出字节)，每组仅在偏移表占 8 字节；1 亿项无每项 8
  字节的固定开销。
- 全量解码单遍 O(字节数)；随机访问 O(1) 组定位 + O(组大小) 解码。

## 参数

```
--generate N     生成 N 个固定种子的递增整数（默认 10000）
--values "..."   逗号分隔的整数序列（可含负数、递减）
--roundtrip      编码后解码并比对
--at N           随机访问第 N 项（0 起），只解所在组
```

## 输出

```
count=… bytes=… bytes_per_item=…
roundtrip=true|false
at=… value=…
```

## 目录

```
delta.go          编解码、分帧、校验与随机访问
cmd/delta-encode  命令行入口
delta_test.go     go test 用例
```
