// Command delta-encode 演示差分编码。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
			var value int64
			if _, err := fmt.Sscan(field, &value); err != nil {
				fmt.Fprintln(stderr, "不是整数")
				return 2
			}
			data = append(data, value)
		}
	} else {
		data = values(*generate)
	}
	encoded := deltaencode.Encode(data)
	fmt.Fprintf(stdout, "count=%d bytes=%d bytes_per_item=%.2f\n", len(data), len(encoded), float64(len(encoded))/float64(len(data)))
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
	return 0
}

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}
