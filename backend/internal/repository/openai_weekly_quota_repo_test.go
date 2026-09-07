package repository

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllocateUpstreamWeeklyDelta(t *testing.T) {
	prior := map[int64]float64{1: 800, 2: 100}
	current := map[int64]float64{1: 810, 2: 130, 3: 10}
	shares := allocateUpstreamWeeklyDelta(5, prior, current)
	require.Equal(t, map[int64]float64{1: 1, 2: 3, 3: 1}, shares)
	// Dollar resets cannot affect weights: the allocator reads immutable logs.
	require.Empty(t, allocateUpstreamWeeklyDelta(0, prior, current))
	require.Empty(t, allocateUpstreamWeeklyDelta(-1, prior, current))
	require.Empty(t, allocateUpstreamWeeklyDelta(math.NaN(), prior, current))
	require.Empty(t, allocateUpstreamWeeklyDelta(1, current, current))
	require.Equal(t, 0.0, allocateUpstreamWeeklyDelta(1, map[int64]float64{1: 9}, map[int64]float64{1: 8, 2: 4})[1])
	thirds := allocateUpstreamWeeklyDelta(1, nil, map[int64]float64{1: 1, 2: 1, 3: 1})
	require.LessOrEqual(t, thirds[1]+thirds[2]+thirds[3], 1.0)
}
