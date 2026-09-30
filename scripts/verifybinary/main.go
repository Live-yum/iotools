// Verify the portable artifact has no application runtime/shared-library imports.
package main

import (
	"debug/elf"
	"debug/pe"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		panic("usage: verifybinary PATH")
	}
	path := os.Args[1]
	if strings.HasSuffix(strings.ToLower(path), ".exe") {
		f, e := pe.Open(path)
		if e != nil {
			panic(e)
		}
		defer f.Close()
		if f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			panic("Windows artifact must be AMD64")
		}
		libs, e := f.ImportedLibraries()
		if e != nil {
			panic(e)
		}
		system := " kernel32.dll advapi32.dll bcrypt.dll dnsapi.dll iphlpapi.dll ntdll.dll secur32.dll shell32.dll userenv.dll ws2_32.dll winmm.dll crypt32.dll ole32.dll user32.dll "
		for _, lib := range libs {
			if !strings.Contains(system, " "+strings.ToLower(lib)+" ") {
				panic("non-system DLL dependency: " + lib)
			}
		}
		fmt.Println("Verified Windows AMD64 PE: system DLLs only", libs)
		return
	}
	f, e := elf.Open(path)
	if e != nil {
		panic(e)
	}
	defer f.Close()
	if f.Machine != elf.EM_AARCH64 && f.Machine != elf.EM_X86_64 {
		panic("unexpected Linux artifact architecture")
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			panic("ELF dynamically linked interpreter present")
		}
	}
	libs, e := f.ImportedLibraries()
	if e != nil {
		panic(e)
	}
	if len(libs) != 0 {
		panic(fmt.Sprint("ELF shared library imports: ", libs))
	}
	fmt.Println("Verified static Linux ELF", f.Machine)
}
