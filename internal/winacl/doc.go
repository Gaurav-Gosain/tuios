// Package winacl checks who may read or change a file on Windows, the
// Windows form of a Unix mode check such as mode&022. It reads a file's owner
// and DACL from an open handle, and names the first account outside a trusted
// set that an allow entry grants some access to.
//
// tuios-web checks its password file with it, and the opencode reader checks
// service.json. Both refuse a file another user could change. On other
// systems the package is empty.
package winacl
