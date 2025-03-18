package util

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
)

// GetStableRandomGenerator returns a stable random generator. Uses the FNV-1a hash algorithm to convert the seed
// string into an int64 for use as seed to the non-cryptographic random number generator.
func GetStableRandomGenerator(seed string) (*rand.Rand, error) {
	hash := fnv.New64a()

	_, err := io.WriteString(hash, seed)
	if err != nil {
		return nil, err
	}

	return rand.New(rand.NewSource(int64(hash.Sum64()))), nil
}

// GetStableRandomInt64FromList returns a stable random value from a given list.
func GetStableRandomInt64FromList(seed int64, list []int64) (int64, error) {
	if len(list) <= 0 {
		return 0, fmt.Errorf("Cannot get stable random value from empty list")
	}

	r, err := GetStableRandomGenerator(fmt.Sprintf("%d", seed))
	if err != nil {
		return 0, fmt.Errorf("Failed to get stable random generator: %w", err)
	}

	return list[r.Int63n(int64(len(list)))], nil
}

// GenerateSequenceInt64 returns a sequence within a given range with given steps.
func GenerateSequenceInt64(begin, end, step int) ([]int64, error) {
	if step == 0 {
		return []int64{}, errors.New("Step must not be zero")
	}

	count := 0
	if (end > begin && step > 0) || (end < begin && step < 0) {
		count = (end-step-begin)/step + 1
	}

	sequence := make([]int64, count)
	for i := 0; i < count; i, begin = i+1, begin+step {
		sequence[i] = int64(begin)
	}

	return sequence, nil
}
