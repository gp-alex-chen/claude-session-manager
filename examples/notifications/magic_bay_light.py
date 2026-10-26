"""TheOne attention_changed consumer for Lenovo Magic Bay Light (Windows).

Python standard library only. stdin is one UTF-8 event; --on/--off/--check are
explicit manual diagnostics. Normal configuration writes are idempotent and
limited to one 16-byte Report 1 per normal invocation. A test lights briefly
and restores its starting state (at most two writes). No firmware/OTA commands.
"""

import argparse
import ctypes as c
from ctypes import wintypes as w
import json
import os
import sys
import time


class GUID(c.Structure):
    _fields_ = [("a", w.DWORD), ("b", w.WORD), ("d", w.WORD), ("e", c.c_ubyte * 8)]


class Interface(c.Structure):
    _fields_ = [("size", w.DWORD), ("guid", GUID), ("flags", w.DWORD), ("reserved", c.c_void_p)]


class Attributes(c.Structure):
    _fields_ = [("size", w.ULONG), ("vendor", w.USHORT), ("product", w.USHORT), ("revision", w.USHORT)]


class Caps(c.Structure):
    _fields_ = [(name, w.WORD) for name in
                ("usage", "usage_page", "input_bytes", "output_bytes", "feature_bytes")]
    _fields_ += [("reserved", w.WORD * 17)]
    _fields_ += [(name, w.WORD) for name in
                ("collections", "input_buttons", "input_values", "input_indices",
                 "output_buttons", "output_values", "output_indices",
                 "feature_buttons", "feature_values", "feature_indices")]


def unique_path(paths):
    if len(paths) > 1:
        raise RuntimeError("More than one matching light/interface; no command sent")
    return paths[0] if paths else None


def validate_identity(info):
    expected = {"vendor": 0x17EF, "product_id": 0x616C, "revision": 0x0101,
                "product": "Magic Bay Light", "manufacturer": "Lenovo",
                "caps": (0xFF00, 1, 16, 0, 60, 2)}
    if info != expected:
        raise ValueError("Unsupported Magic Bay Light identity, revision or HID capabilities; no command sent")


class WindowsLight:
    def __init__(self, hid, kernel, handle):
        self.hid, self.kernel, self.handle = hid, kernel, handle

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.kernel.CloseHandle(self.handle)

    def read(self):
        report = (c.c_ubyte * 16)()
        report[0] = 1
        if not self.hid.HidD_GetFeature(self.handle, report, 16):
            raise c.WinError(c.get_last_error())
        return bytes(report)

    def write(self, payload):
        if len(payload) != 16 or payload[:2] != b"\x01\x01":
            raise ValueError("Only a 16-byte normal Report 1 command is allowed")
        report = (c.c_ubyte * 16).from_buffer_copy(payload)
        if not self.hid.HidD_SetFeature(self.handle, report, 16):
            raise c.WinError(c.get_last_error())


def open_light():
    if os.name != "nt":
        raise OSError("Magic Bay Light control requires Windows")
    hid = c.WinDLL("hid", use_last_error=True)
    setup = c.WinDLL("setupapi", use_last_error=True)
    kernel = c.WinDLL("kernel32", use_last_error=True)
    hid.HidD_GetHidGuid.argtypes = [c.POINTER(GUID)]
    hid.HidD_GetHidGuid.restype = None
    setup.SetupDiGetClassDevsW.argtypes = [c.POINTER(GUID), w.LPCWSTR, w.HWND, w.DWORD]
    setup.SetupDiGetClassDevsW.restype = c.c_void_p
    setup.SetupDiEnumDeviceInterfaces.argtypes = [c.c_void_p, c.c_void_p, c.POINTER(GUID), w.DWORD, c.POINTER(Interface)]
    setup.SetupDiGetDeviceInterfaceDetailW.argtypes = [c.c_void_p, c.POINTER(Interface), c.c_void_p, w.DWORD, c.POINTER(w.DWORD), c.c_void_p]
    setup.SetupDiDestroyDeviceInfoList.argtypes = [c.c_void_p]
    kernel.CreateFileW.argtypes = [w.LPCWSTR, w.DWORD, w.DWORD, c.c_void_p, w.DWORD, w.DWORD, c.c_void_p]
    kernel.CreateFileW.restype = c.c_void_p
    kernel.CloseHandle.argtypes = [c.c_void_p]
    hid.HidD_GetAttributes.argtypes = [c.c_void_p, c.POINTER(Attributes)]
    hid.HidD_GetAttributes.restype = c.c_ubyte
    for name in ("HidD_GetProductString", "HidD_GetManufacturerString", "HidD_GetFeature", "HidD_SetFeature"):
        fn = getattr(hid, name)
        fn.argtypes = [c.c_void_p, c.c_void_p, w.ULONG]
        fn.restype = c.c_ubyte
    hid.HidD_GetPreparsedData.argtypes = [c.c_void_p, c.POINTER(c.c_void_p)]
    hid.HidD_GetPreparsedData.restype = c.c_ubyte
    hid.HidD_FreePreparsedData.argtypes = [c.c_void_p]
    hid.HidD_FreePreparsedData.restype = c.c_ubyte
    hid.HidP_GetCaps.argtypes = [c.c_void_p, c.POINTER(Caps)]
    hid.HidP_GetCaps.restype = w.LONG

    guid = GUID()
    hid.HidD_GetHidGuid(c.byref(guid))
    devices = setup.SetupDiGetClassDevsW(c.byref(guid), None, None, 0x12)
    if devices == c.c_void_p(-1).value:
        raise c.WinError(c.get_last_error())
    paths = []
    try:
        for i in range(256):
            interface = Interface()
            interface.size = c.sizeof(interface)
            if not setup.SetupDiEnumDeviceInterfaces(devices, None, c.byref(guid), i, c.byref(interface)):
                if c.get_last_error() == 259:
                    break
                raise c.WinError(c.get_last_error())
            size = w.DWORD()
            setup.SetupDiGetDeviceInterfaceDetailW(devices, c.byref(interface), None, 0, c.byref(size), None)
            data = c.create_string_buffer(size.value)
            c.cast(data, c.POINTER(w.DWORD))[0] = 8 if c.sizeof(c.c_void_p) == 8 else 6
            if not setup.SetupDiGetDeviceInterfaceDetailW(devices, c.byref(interface), data, size.value, None, None):
                raise c.WinError(c.get_last_error())
            path = c.wstring_at(c.addressof(data) + 4)
            if "vid_17ef&pid_616c" in path.lower():
                paths.append(path)
        else:
            raise RuntimeError("Device enumeration limit reached; no command sent")
    finally:
        setup.SetupDiDestroyDeviceInfoList(devices)
    path = unique_path(paths)
    if path is None:
        return None
    # Zero access is sufficient for this device's HID control requests.
    handle = kernel.CreateFileW(path, 0, 3, None, 3, 0, None)
    if handle == c.c_void_p(-1).value:
        raise c.WinError(c.get_last_error())
    try:
        attrs = Attributes()
        attrs.size = c.sizeof(attrs)
        if not hid.HidD_GetAttributes(handle, c.byref(attrs)):
            raise c.WinError(c.get_last_error())
        product, manufacturer = c.create_unicode_buffer(256), c.create_unicode_buffer(256)
        if not hid.HidD_GetProductString(handle, product, c.sizeof(product)) or not hid.HidD_GetManufacturerString(handle, manufacturer, c.sizeof(manufacturer)):
            raise RuntimeError("Cannot read light identity")
        pp = c.c_void_p()
        if not hid.HidD_GetPreparsedData(handle, c.byref(pp)):
            raise RuntimeError("Cannot read HID capabilities")
        try:
            caps = Caps()
            if hid.HidP_GetCaps(pp, c.byref(caps)) != 0x00110000:
                raise RuntimeError("Cannot parse HID capabilities")
            validate_identity({"vendor": attrs.vendor, "product_id": attrs.product, "revision": attrs.revision,
                               "product": product.value, "manufacturer": manufacturer.value,
                               "caps": (caps.usage_page, caps.usage, caps.input_bytes, caps.output_bytes,
                                        caps.feature_bytes, caps.feature_buttons)})
        finally:
            hid.HidD_FreePreparsedData(pp)
    except BaseException:
        kernel.CloseHandle(handle)
        raise
    return WindowsLight(hid, kernel, handle)


def validate_status(status):
    if (len(status) != 16 or status[:2] != b"\x01\x02" or status[4] not in range(4)
            or status[5] not in (0, 1) or status[6] not in (0, 1)
            or status[7:9] != b"\x01\x01" or status[11] not in (0, 1)):
        raise ValueError("Unsupported light status/firmware format; no further command sent")


def write_state(device, status, level, enabled):
    target = bytearray(status)
    target[1], target[4], target[6] = 1, level, int(enabled)
    # Reply byte 7 is firmware version. Command byte 7 instead holds the
    # auxiliary config flag returned at reply byte 11. Preserve that flag.
    target[7] = status[11]
    device.write(bytes(target))
    after = device.read()
    validate_status(after)
    if after[4] != target[4] or after[6] != target[6] or after[5] != status[5] or after[11] != status[11]:
        raise RuntimeError("Light did not confirm requested state; no retry sent")


def set_light(active, opener=open_light):
    device = opener()
    if device is None:
        return {"present": False, "changed": False}
    with device:
        status = device.read()
        validate_status(status)
        matches = (status[4] > 0 and status[6] == 1) if active else (status[4] == 0 and status[6] == 0)
        if matches:
            return {"present": True, "changed": False, "active": active}
        write_state(device, status, 1 if active else 0, active)
        return {"present": True, "changed": True, "active": active}


def test_light(opener, wait_fn):
    device = opener()
    if device is None:
        raise RuntimeError("Magic Bay Light is not connected; test could not light it")
    with device:
        before = device.read()
        validate_status(before)
        if before[4] > 0 and before[6] == 1:
            return {"test": True, "present": True, "changed": False, "already_on": True}
        try:
            write_state(device, before, 1, True)
            wait_fn(1.0)
        finally:
            current = device.read()
            validate_status(current)
            if (current[4], current[6]) != (before[4], before[6]):
                write_state(device, current, before[4], bool(before[6]))
        return {"test": True, "present": True, "changed": True, "restored": True}


def handle_event(event, opener=open_light, wait_fn=time.sleep):
    if not isinstance(event, dict):
        raise ValueError("Expected one JSON object")
    if event.get("event") in ("input_requested", "permission_requested", "task_completed") and event.get("test") is True:
        if event.get("schema_version") != 1:
            raise ValueError("Expected schema_version 1")
        return test_light(opener, wait_fn)
    if event.get("event") != "attention_changed":
        return {"ignored": True}
    state = event.get("attention")
    if event.get("schema_version") != 1 or not isinstance(state, dict):
        raise ValueError("Expected schema_version 1 and attention state")
    active, waiting, unread = (state.get(k) for k in ("active", "waiting", "unread_count"))
    if (type(active) is not bool or type(waiting) is not bool or type(unread) is not int
            or unread < 0 or active != (waiting or unread > 0) or type(event.get("test", False)) is not bool):
        raise ValueError("Invalid or inconsistent attention state")
    if event.get("test", False):
        return test_light(opener, wait_fn)
    return set_light(active, opener)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--on", action="store_true", help="Explicitly set minimum nonzero brightness")
    group.add_argument("--off", action="store_true", help="Explicitly turn the light off")
    group.add_argument("--check", action="store_true", help="Read identity and status without writing")
    args = parser.parse_args()
    try:
        if args.check:
            device = open_light()
            result = {"present": device is not None}
            if device is not None:
                with device:
                    status = device.read(); validate_status(status)
                    result.update(level=status[4], enabled=bool(status[6]), auxiliary_flag=status[11])
        elif args.on or args.off:
            result = set_light(args.on)
        else:
            raw = sys.stdin.buffer.read((64 << 10) + 1)
            if len(raw) > 64 << 10:
                raise ValueError("Notification exceeds 64 KiB")
            result = handle_event(json.loads(raw.decode("utf-8-sig")))
        print(json.dumps(result, ensure_ascii=False))
        return 0
    except (OSError, ValueError, RuntimeError) as error:
        print(str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
