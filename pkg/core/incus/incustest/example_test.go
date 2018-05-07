package incustest_test

import (
	"fmt"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

// ExampleMockBackend shows the default stateful behavior: lifecycle methods
// track containers in MockBackend.Containers.
func ExampleMockBackend() {
	mock := incustest.NewMockBackend()

	_ = mock.CreateContainer(incus.ContainerConfig{
		Name:   "demo",
		CPU:    "2",
		Memory: "4GB",
	})
	_ = mock.StartContainer("demo")

	got, _ := mock.GetContainer("demo")
	fmt.Println(got.Name, got.State)
	// Output: demo Running
}

// ExampleMockBackend_overrideHook shows how to override a single method via
// its *Func hook, useful for simulating errors or custom responses.
func ExampleMockBackend_overrideHook() {
	mock := incustest.NewMockBackend()
