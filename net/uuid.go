package net

import (
	"io/ioutil"
	"net"
	"os"

	"github.com/weaveworks/mesh"
	"github.com/weaveworks/weave/db"
)

func getOldStyleSystemUUID() ([]byte, error) {
	uuid, err := ioutil.ReadFile("/sys/class/dmi/id/product_uuid")
	if os.IsNotExist(err) {
		uuid, err = ioutil.ReadFile("/sys/hypervisor/uuid")
	}
	return uuid, err
}

func getSystemUUID(hostRoot string) ([]byte, error) {
	uuid, err := getOldStyleSystemUUID()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	machineid, err := ioutil.ReadFile(hostRoot + "/etc/machine-id")
	if os.IsNotExist(err) {
		machineid, _ = ioutil.ReadFile(hostRoot + "/var/lib/dbus/machine-id")
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return append(machineid, uuid...), nil
}

func getPersistedPeerName(dbPrefix string) mesh.PeerName {
	d, err := db.NewBoltDBReadOnly(dbPrefix + "data.db")
	if err != nil {
		return mesh.UnknownPeerName
	}
	defer d.Close()
	var peerName mesh.PeerName
	nameFound, err := d.Load(db.NameIdent, &peerName)
	if err != nil || !nameFound {
		return mesh.UnknownPeerName
	}
	return peerName
}

// GetSystemPeerName returns an ID derived from concatenated machine-id
// (either systemd or dbus), the system (aka bios) UUID and the
// hypervisor UUID.  It is tweaked and formatted to be usable as a mac address
func GetSystemPeerName(dbPrefix, hostRoot string) (string, error) {
	// Check if we have a persisted name that matches the old-style ID for this host
	if oldUUID, err := getOldStyleSystemUUID(); err == nil {
		persistedPeerName := getPersistedPeerName(dbPrefix)
		if persistedPeerName == mesh.PeerNameFromBin(PersistentMAC(oldUUID)) {
			return persistedPeerName.String(), nil
		}
	}
	var mac net.HardwareAddr
	if uuid, err := getSystemUUID(hostRoot); err == nil && len(uuid) > 0 {
		mac = PersistentMAC(uuid)
	} else {
		// It's a bit worrying that we silently drop any error from getSystemUUID
		mac, err = RandomMAC()
		if err != nil {
			return "", err
		}
	}
	return mac.String(), nil
}
