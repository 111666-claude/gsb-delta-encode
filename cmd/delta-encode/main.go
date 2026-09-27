// Command delta-encode 演示分帧差分编码。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"example.com/deltaencode"
)

// values 生成固定种子的递增序列。
func values(count int) []int64 {
	out := make([]int64, 0, count)
	state := uint64(20260927)
	current := int64(0)
	for index := 0; index < count; index++ {
		state = state*6364136223846793005 + 1442695040888963407
		current += int64(state>>60) + 1
		out = append(out, current)
	}
	return out
}

// Run 执行一次命令行调用，返回退出码。
func Run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("delta-encode", flag.ContinueOnError)
	flags.SetOutput(stderr)
	generate := flags.Int("generate", 10000, "生成多少个递增整数")
	raw := flags.String("values", "", "逗号分隔的整数序列（可含递减）")
	roundtrip := flags.Bool("roundtrip", false, "编码后再解码并比对")
	at := flags.Int64("at", -1, "随机访问第 N 项（0 起下标），只解它所在的一组")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	var data []int64
	if *raw != "" {
		for _, field := range strings.Split(*raw, ",") {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			value, err := strconv.ParseInt(field, 10, 64)
			if err != nil {
				fmt.Fprintf(stderr, "不是整数：%q\n", field)
				return 2
			}
			data = append(data, value)
		}
	} else {
		data = values(*generate)
	}
	encoded := deltaencode.Encode(data)
	bytesPerItem := 0.0
	if len(data) > 0 {
		bytesPerItem = float64(len(encoded)) / float64(len(data))
	}
	fmt.Fprintf(stdout, "count=%d bytes=%d bytes_per_item=%.2f\n", len(data), len(encoded), bytesPerItem)
	if *roundtrip {
		decoded, err := deltaencode.Decode(encoded)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		same := len(decoded) == len(data)
		if same {
			for index := range data {
				if data[index] != decoded[index] {
					same = false
					break
				}
			}
		}
		fmt.Fprintf(stdout, "roundtrip=%v\n", same)
		if !same {
			return 1
		}
	}
	if *at >= 0 {
		value, err := deltaencode.LookupAt(encoded, *at)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "at=%d value=%d\n", *at, value)
	}
	return 0
}

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}
