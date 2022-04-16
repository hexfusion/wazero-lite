package replace_import

import (
	_ "embed"
	"fmt"
	"log"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// main shows how you can replace a module import when it doesn't match instantiated modules.
func main() {
	r := wazero.NewRuntime()

	// Instantiate a function that closes the module under "assemblyscript.abort".
	host, err := r.NewModuleBuilder("assemblyscript").
		ExportFunction("abort", func(m api.Module, messageOffset, fileNameOffset, line, col uint32) {
			_ = m.CloseWithExitCode(255)
		}).Instantiate()
	if err != nil {
		log.Fatal(err)
	}
	defer host.Close()

	// Compile code that needs the function "env.abort".
	code, err := r.CompileModule([]byte(`(module $needs-import
	(import "env" "abort" (func $~lib/builtins/abort (param i32 i32 i32 i32)))

	(export "abort" (func 0)) ;; exports the import for testing
)`))
	if err != nil {
		log.Fatal(err)
	}
	defer code.Close()

	// Instantiate the module, replacing the import "env.abort" with "assemblyscript.abort".
	mod, err := r.InstantiateModuleWithConfig(code, wazero.NewModuleConfig().
		WithImport("env", "abort", "assemblyscript", "abort"))
	if err != nil {
		log.Fatal(err)
	}
	defer mod.Close()

	// Since the above worked, the exported function closes the module.
	_, err = mod.ExportedFunction("abort").Call(nil, 0, 0, 0, 0)
	fmt.Println(err)
}