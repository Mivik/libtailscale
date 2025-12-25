package main

// #include <jni.h>
import "C"

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/tailscale/libtailscale/jni"
	"tailscale.com/net/netmon"
)

func getInterfaces(jvm *jni.JVM, appCtx jni.Object) ([]netmon.Interface, error) {
	var ifaces []netmon.Interface
	var ifaceString string
	err := jni.Do(jvm, func(env *jni.Env) error {
		cls := jni.GetObjectClass(env, appCtx)
		m := jni.GetMethodID(env, cls, "getInterfacesAsString", "()Ljava/lang/String;")
		n, err := jni.CallObjectMethod(env, appCtx, m)
		ifaceString = jni.GoString(env, jni.String(n))
		return err
	})
	if err != nil {
		return ifaces, err
	}

	for _, iface := range strings.Split(ifaceString, "\n") {
		// Example of the strings we're processing:
		// wlan0 30 1500 true true false false true | fe80::2f60:2c82:4163:8389%wlan0/64 10.1.10.131/24
		// r_rmnet_data0 21 1500 true false false false false | fe80::9318:6093:d1ad:ba7f%r_rmnet_data0/64
		// mnet_data2 12 1500 true false false false false | fe80::3c8c:44dc:46a9:9907%rmnet_data2/64

		if strings.TrimSpace(iface) == "" {
			continue
		}

		fields := strings.Split(iface, "|")
		if len(fields) != 2 {
			log.Printf("getInterfaces: unable to split %q", iface)
			continue
		}

		var name string
		var index, mtu int
		var up, broadcast, loopback, pointToPoint, multicast bool
		_, err := fmt.Sscanf(fields[0], "%s %d %d %t %t %t %t %t",
			&name, &index, &mtu, &up, &broadcast, &loopback, &pointToPoint, &multicast)
		if err != nil {
			log.Printf("getInterfaces: unable to parse %q: %v", iface, err)
			continue
		}

		newIf := netmon.Interface{
			Interface: &net.Interface{
				Name:  name,
				Index: index,
				MTU:   mtu,
			},
			AltAddrs: []net.Addr{}, // non-nil to avoid Go using netlink
		}
		if up {
			newIf.Flags |= net.FlagUp
		}
		if broadcast {
			newIf.Flags |= net.FlagBroadcast
		}
		if loopback {
			newIf.Flags |= net.FlagLoopback
		}
		if pointToPoint {
			newIf.Flags |= net.FlagPointToPoint
		}
		if multicast {
			newIf.Flags |= net.FlagMulticast
		}

		addrs := strings.Trim(fields[1], " \n")
		for _, addr := range strings.Split(addrs, " ") {
			pfx, err := netip.ParsePrefix(addr)
			var ip net.IP
			if pfx.Addr().Is4() {
				v4 := pfx.Addr().As4()
				ip = net.IP(v4[:])
			} else {
				v6 := pfx.Addr().As16()
				ip = net.IP(v6[:])
			}
			if err == nil {
				newIf.AltAddrs = append(newIf.AltAddrs, &net.IPAddr{
					IP:   ip,
					Zone: pfx.Addr().Zone(),
				})
			}
		}

		ifaces = append(ifaces, newIf)
	}

	return ifaces, nil
}

//export TsnetRegisterAndroidInterface
func TsnetRegisterAndroidInterface(envPtr unsafe.Pointer, appCtxPtr unsafe.Pointer) int {
	env := (*jni.Env)(envPtr)
	appCtx := jni.NewGlobalRef(env, jni.Object(appCtxPtr))
	jvm, err := jni.GetJVM(env)
	if err != nil {
		return -1
	}

	{
		appCls := jni.GetObjectClass(env, appCtx)
		m := jni.GetMethodID(env, appCls, "getCacheDir", "()Ljava/io/File;")
		cacheDir, err := jni.CallObjectMethod(env, appCtx, m)
		if err != nil {
			return -1
		}
		fileCls := jni.GetObjectClass(env, cacheDir)
		m = jni.GetMethodID(env, fileCls, "getAbsolutePath", "()Ljava/lang/String;")
		cacheString, err := jni.CallObjectMethod(env, cacheDir, m)
		if err != nil {
			return -1
		}
		cachePath := jni.GoString(env, jni.String(cacheString))
		logsDir := filepath.Join(cachePath, "ts-logs")
		err = os.MkdirAll(logsDir, os.ModePerm)
		if err != nil {
			return -1
		}
		os.Setenv("TS_LOGS_DIR", logsDir)
	}

	// Register the interface getter with netmon
	netmon.RegisterInterfaceGetter(func() ([]netmon.Interface, error) {
		return getInterfaces(jvm, appCtx)
	})

	return 0
}
