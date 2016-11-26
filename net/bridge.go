package net

import (
	"fmt"
	"io/ioutil"

	"github.com/vishvananda/netlink"
)

type BridgeType int

const (
	WeaveBridgeName = "weave"
	DatapathName    = "datapath"

	None BridgeType = iota
	Bridge
	Fastdp
	BridgedFastdp
	Inconsistent
)

func DetectBridgeType(weaveBridgeName, datapathName string) BridgeType {
	bridge, _ := netlink.LinkByName(weaveBridgeName)
	datapath, _ := netlink.LinkByName(datapathName)

	switch {
	case bridge == nil && datapath == nil:
		return None
	case isBridge(bridge) && datapath == nil:
		return Bridge
	case isDatapath(bridge) && datapath == nil:
		return Fastdp
	case isDatapath(datapath) && isBridge(bridge):
		return BridgedFastdp
	default:
		return Inconsistent
	}
}

func EnforceDockerBridgeAddrAssignType(bridgeName string) error {
	addrAssignType, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/addr_assign_type", bridgeName))
	if err != nil {
		return err
	}

	// From include/uapi/linux/netdevice.h
	// #define NET_ADDR_PERM       0   /* address is permanent (default) */
	// #define NET_ADDR_RANDOM     1   /* address is generated randomly */
	// #define NET_ADDR_STOLEN     2   /* address is stolen from other device */
	// #define NET_ADDR_SET        3   /* address is set using dev_set_mac_address() */
	// Note the file typically has a newline at the end, so we just look at the first char
	if addrAssignType[0] != '3' {
		link, err := netlink.LinkByName(bridgeName)
		if err != nil {
			return err
		}

		mac, err := RandomMAC()
		if err != nil {
			return err
		}

		if err := netlink.LinkSetHardwareAddr(link, mac); err != nil {
			return err
		}
	}

	return nil
}

func isBridge(link netlink.Link) bool {
	_, isBridge := link.(*netlink.Bridge)
	return isBridge
}

func isDatapath(link netlink.Link) bool {
	switch link.(type) {
	case *netlink.GenericLink:
		return link.Type() == "openvswitch"
	case *netlink.Device:
		// Assume it's our openvswitch device, and the kernel has not been updated to report the kind.
		return true
	default:
		return false
	}
}
