package utils_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BiJie/BinanceChain/common/utils"
)

func TestConcurrentExecuteAsync(t *testing.T) {
	var nums = map[int][]int{}
	for i := 0; i < 100; i++ {
		nums[i] = []int{0}
	}

	sum := 0
	numCh := make(chan int, 4)
	producer := func() {
		for i := 0; i < 100; i++ {
			numCh <- i
		}
		close(numCh)
	}
	consumer := func() {
		for num := range numCh {
			nums[num][0] = num
		}
	}
	postConsume := func() {
		for _, numArr := range nums {
			sum += numArr[0]
		}
	}
	utils.ConcurrentExecuteAsync(4, producer, consumer, postConsume)
	require.NotEqual(t, 4950, sum)
	time.Sleep(1e6)
	require.Equal(t, 4950, sum)
	for num, numArr := range nums {
		require.Equal(t, num, numArr[0])
	}
}

func TestConcurrentExecuteSync(t *testing.T) {
	var nums = map[int][]int{}
	for i := 0; i < 100; i++ {
		nums[i] = []int{0}
	}

	sum := 0
	numCh := make(chan int, 4)
	producer := func() {
		for i := 0; i < 100; i++ {
			numCh <- i
		}
		close(numCh)
	}
	consumer := func() {
		for num := range numCh {
			nums[num][0] = num
		}
	}
	utils.ConcurrentExecuteSync(4, producer, consumer)
	for num, numArr := range nums {
		require.Equal(t, num, numArr[0])
	}
	for _, numArr := range nums {
		sum += numArr[0]
	}
	require.Equal(t, 4950, sum)
}