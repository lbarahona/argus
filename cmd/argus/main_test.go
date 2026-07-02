package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExitCodeFor_Nil(t *testing.T) {
	assert.Equal(t, 0, exitCodeFor(nil))
}

func TestExitCodeFor_PlainError(t *testing.T) {
	assert.Equal(t, 1, exitCodeFor(errors.New("boom")))
}

func TestExitCodeFor_ExitError(t *testing.T) {
	assert.Equal(t, 2, exitCodeFor(exitError{code: 2}))
}

func TestExitCodeFor_WrappedExitError(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", exitError{code: 2})
	assert.Equal(t, 2, exitCodeFor(err))
}

func TestExitError_EmptyMessage(t *testing.T) {
	assert.Equal(t, "", exitError{code: 2}.Error())
}
