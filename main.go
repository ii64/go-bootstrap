package main

import (
	"bufio"
	"bytes"
	"debug/elf"
	"debug/gosym"
	"encoding/hex"
	"fmt"
	"github.com/ii64/go-bootstrap/internal/stub"
	"io"
	"log"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var _ = reflect.ValueOf(stub.DisallowInternalReplacer)

var logger = getLogger()

func getGoCmd() (r string) {
	r = os.Getenv("BOOTSTRAP_GO")
	if r != "" {
		return
	}
	return "go"
}

func getLogger() *log.Logger {
	if os.Getenv("BOOTSTRAP_DEBUG") != "" {
		return log.New(os.Stdout, "", log.LstdFlags)
	}
	return log.New(io.Discard, "", log.LstdFlags)
}

func main() {
	var err error
	args := os.Args[1:]

	logger.Println("spawining go", args)
	c := exec.Command(getGoCmd(), args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	err = c.Start()
	if err != nil {
		panic(err)
	}

	pid := c.Process.Pid
	logger.Println("spawn pid:", pid)

	logger.Println("suspending...")
	err = c.Process.Signal(syscall.SIGSTOP)
	if err != nil {
		panic(err)
	}

	// patching
	var selfSymtab *gosym.Table
	selfSymtab, err = getSelfSymtab()
	if err != nil {
		panic(err)
	}
	var fnSrc *gosym.Func
	for _, iterFn := range selfSymtab.Funcs {
		if strings.Contains(iterFn.Name, "DisallowInternalReplacer") {
			logger.Println("found replacer:", iterFn.Name)
			fnSrc = &iterFn
			break
		}
	}
	if fnSrc == nil {
		panic("src stub not found")
	}
	srcFn := uintptr(fnSrc.Entry)
	srcFnSz := int(fnSrc.End - fnSrc.Entry)
	srcProg := bytesliceFrom(srcFn, srcFnSz)
	logger.Println("replacer info:", srcFn, srcFnSz)
	logger.Println("replacer prog:", hex.EncodeToString(srcProg))

	var baseAddr uintptr
	baseAddr, err = getProcBaseAddress(pid)
	if err != nil {
		panic(err)
	}
	logger.Println("target base:", baseAddr)
	logger.Println("target path:", c.Path)

	var fprog *os.File
	var n int
	fprog, err = getProcessMem(pid)
	if err != nil {
		panic(err)
	}
	defer fprog.Close()

	var tab *gosym.Table
	var dmpTab = make([]byte, 0x10000000)
	n, err = pRead(fprog, dmpTab, baseAddr)
	if err != nil {
		panic(err)
	}
	dmpTab = dmpTab[:n]

	tab, err = getSymtab(bytes.NewReader(dmpTab))
	if err != nil {
		panic(err)
	}

	var fn *gosym.Func
	for _, iterFn := range tab.Funcs {
		if strings.Contains(iterFn.Name, "disallowInternal") {
			fn = &iterFn
			logger.Println("found target:", fn.Name)
			break
		}
	}
	if fn == nil {
		panic("disallowInternal func not found.")
	}

	targetFn := uintptr(fn.Entry)
	targetFnSz := int(fn.End - fn.Entry)
	logger.Println("target info", targetFn, targetFnSz)

	targetFnDmp := make([]byte, targetFnSz)
	n, err = pRead(fprog, targetFnDmp, targetFn)
	if err != nil {
		panic(err)
	}
	logger.Println("dmp target info:", n)
	targetFnDmp = targetFnDmp[:n]
	if false {
		fmt.Println(hex.EncodeToString(targetFnDmp))
	}

	if true {
		n, err = pWrite(fprog, srcProg, targetFn) // write the replacer
		if err != nil {
			panic(err)
		}
		logger.Println("pwrite ret:", n)
	}

	// resume process.
	logger.Println("resuming...")
	err = c.Process.Signal(syscall.SIGCONT)
	if err != nil {
		return
	}

	c.Wait()
	logger.Println("done.")
}

func getProcessMem(pid int) (f *os.File, err error) {
	var path = "/proc/self/mem"
	if pid > 0 {
		path = fmt.Sprintf("/proc/%d/mem", pid)
	}
	f, err = os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return
	}
	return
}
func pRead(f *os.File, data []byte, offset uintptr) (n int, err error) {
	fd := int(f.Fd())
	return syscall.Pread(fd, data, int64(offset))
}
func pWrite(f *os.File, data []byte, offset uintptr) (n int, err error) {
	fd := int(f.Fd())
	return syscall.Pwrite(fd, data, int64(offset))
}

func getProcBaseAddress(pid int) (uintptr, error) {
	var path = "/proc/self/maps"
	if pid > 0 {
		path = fmt.Sprintf("/proc/%d/maps", pid)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var addrStr []byte
	addrStr, err = reader.ReadBytes('-')
	if err != nil {
		return 0, err
	}
	addrStr = addrStr[:len(addrStr)-1]

	var addr uint64
	addr, err = strconv.ParseUint(string(addrStr), 16, 64)
	if err != nil {
		return 0, err
	}
	return uintptr(addr), nil
}

// ----

func getSelfSymtab() (tab *gosym.Table, err error) {
	var baseAddr uintptr
	baseAddr, err = getProcBaseAddress(0)
	if err != nil {
		return
	}
	tab, err = getSymtab(bytes.NewReader(bytesliceFrom(baseAddr, 0x10000000)))
	return
}

func getSymtab(r io.ReaderAt) (tab *gosym.Table, err error) {
	var f *elf.File
	f, err = elf.NewFile(r)
	if err != nil {
		return
	}
	defer f.Close()

	section := f.Section(".text")
	txtBeginAddr := section.Addr
	section = f.Section(".gopclntab")
	var pclntabData []byte
	pclntabData, err = section.Data()
	if err != nil {
		return
	}
	ltab := gosym.NewLineTable(pclntabData, txtBeginAddr)
	tab, err = gosym.NewTable([]byte{}, ltab)
	if err != nil {
		return
	}
	return
}

func bytesliceFrom(baseAddr uintptr, sz int) []byte {
	return *(*[]byte)(unsafe.Pointer(&reflect.SliceHeader{
		Data: baseAddr,
		Len:  sz,
		Cap:  sz,
	}))
}
