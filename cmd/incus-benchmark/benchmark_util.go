package main

import (
	"fmt"
	"time"
)

func getContainerName(count int, index int) string {
	nameFormat := "benchmark-%." + fmt.Sprintf("%d", len(fmt.Sprintf("%d", count))) + "d"
	return fmt.Sprintf(nameFormat, index+1)
}

func logf(format string, args ...any) {
	fmt.Printf(fmt.Sprintf("[%s] %s\n", time.Now().Format(time.StampMilli), format), args...)
}

func printTestConfig(count int, batchSize int, image string, privileged bool, freeze bool) {
	privilegedStr := "unprivileged"
	if privileged {
		privilegedStr = "privileged"
	}

	mode := "normal startup"
	if freeze {
		mode = "start and freeze"
	}

	batches := count / batchSize
	remainder := count % batchSize
	fmt.Println("Test variables:")
	fmt.Println("  Container count:", count)
	fmt.Println("  Container mode:", privilegedStr)
	fmt.Println("  Startup mode:", mode)
	fmt.Println("  Image:", image)
	fmt.Println("  Batches:", batches)
	fmt.Println("  Batch size:", batchSize)
	fmt.Println("  Remainder:", remainder)
	fmt.Println("")
}
