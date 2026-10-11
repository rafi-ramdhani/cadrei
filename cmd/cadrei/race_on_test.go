//go:build race

package main

// raceOn says the race detector is on: timings and memory aren't the real
// program's then.
const raceOn = true
