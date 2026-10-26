import contextlib
import unittest

import magic_bay_light as lamp


OFF = bytes.fromhex("01020000000000010100400000000000")


class FakeLight(contextlib.AbstractContextManager):
    def __init__(self, status=OFF, update=True):
        self.status = status
        self.update = update
        self.writes = []

    def __exit__(self, *args):
        pass

    def read(self):
        return self.status

    def write(self, payload):
        self.writes.append(payload)
        if self.update:
            status = bytearray(self.status)
            status[4:7] = payload[4:7]
            status[11] = payload[7]
            self.status = bytes(status)


def event(active, waiting=False, unread=0, **extra):
    return {"schema_version": 1, "event": "attention_changed", "test": False,
            "attention": {"active": active, "waiting": waiting, "unread_count": unread}, **extra}


class LightTests(unittest.TestCase):
    def test_turn_on_and_off_preserve_flags_and_use_short_normal_report(self):
        device = FakeLight()
        result = lamp.handle_event(event(True, waiting=True), opener=lambda: device)
        self.assertTrue(result["changed"])
        # Response byte 7 is firmware version, NOT command byte 7's config flag.
        self.assertEqual(device.writes, [bytes.fromhex("01010000010001000100400000000000")])
        lamp.handle_event(event(False), opener=lambda: device)
        self.assertEqual(device.writes[-1], bytes.fromhex("01010000000000000100400000000000"))
        self.assertEqual(len(device.writes), 2)

    def test_duplicate_force_reads_without_wearing_configuration_storage(self):
        for level in (1, 2, 3):
            status = bytearray(OFF); status[4] = level; status[6] = 1
            device = FakeLight(bytes(status))
            for _ in range(3):
                self.assertFalse(lamp.handle_event(event(True, unread=2, force=True), opener=lambda: device)["changed"])
            self.assertEqual(device.writes, [])
        device = FakeLight()
        self.assertFalse(lamp.handle_event(event(False), opener=lambda: device)["changed"])
        self.assertEqual(device.writes, [])

    def test_config_flag_is_mapped_from_response_offset_11(self):
        status = bytearray(OFF); status[11] = 1; status[5] = 1
        device = FakeLight(bytes(status))
        lamp.handle_event(event(True, waiting=True), opener=lambda: device)
        self.assertEqual(device.writes[0][5], 1)
        self.assertEqual(device.writes[0][7], 1)

    def test_missing_device_is_a_successful_noop(self):
        self.assertEqual(lamp.handle_event(event(True, unread=1), opener=lambda: None), {"present": False, "changed": False})

    def test_unrelated_real_events_never_open_hardware(self):
        def forbidden():
            self.fail("opened hardware for a test or unrelated event")
        for kind in ("input_requested", "permission_requested", "task_completed"):
            lamp.handle_event({"event": kind}, opener=forbidden)

    def test_test_event_lights_briefly_then_restores_original_state(self):
        for kind in ("attention_changed", "input_requested", "permission_requested", "task_completed"):
            device = FakeLight()
            waits = []
            payload = event(False, test=True) if kind == "attention_changed" else {"schema_version": 1, "event": kind, "test": True}
            result = lamp.handle_event(payload, opener=lambda: device, wait_fn=waits.append)
            self.assertTrue(result["test"])
            self.assertEqual(waits, [1.0])
            self.assertEqual(len(device.writes), 2)
            self.assertEqual(device.writes[0][4], 1)
            self.assertEqual(device.status, OFF)

    def test_test_event_does_not_change_a_light_already_on_at_higher_brightness(self):
        status = bytearray(OFF); status[4] = 3; status[6] = 1
        device = FakeLight(bytes(status))
        lamp.handle_event(event(True, unread=1, test=True), opener=lambda: device, wait_fn=lambda _: None)
        self.assertEqual(device.writes, [])
        self.assertEqual(device.status, bytes(status))

    def test_test_event_restores_after_wait_failure_and_reports_absent_device(self):
        device = FakeLight()
        def interrupted(_):
            raise RuntimeError("interrupted fixture")
        with self.assertRaisesRegex(RuntimeError, "interrupted fixture"):
            lamp.handle_event(event(False, test=True), opener=lambda: device, wait_fn=interrupted)
        self.assertEqual(device.status, OFF)
        with self.assertRaisesRegex(RuntimeError, "not connected"):
            lamp.handle_event(event(False, test=True), opener=lambda: None, wait_fn=lambda _: None)

    def test_invalid_aggregate_is_rejected_before_hardware_access(self):
        for bad in (event(True), event(False, waiting=True), event(True, unread=-1),
                    event(True, unread=True), event(1, waiting=True), event(True, waiting=1),
                    {"schema_version": 2, "event": "attention_changed"},
                    {"schema_version": 1, "event": "attention_changed", "attention": []}):
            with self.assertRaises(ValueError):
                lamp.handle_event(bad, opener=lambda: self.fail("opened hardware for invalid payload"))

    def test_verification_failure_does_not_retry(self):
        device = FakeLight(update=False)
        with self.assertRaises(RuntimeError):
            lamp.handle_event(event(True, waiting=True), opener=lambda: device)
        self.assertEqual(len(device.writes), 1)

    def test_bad_status_and_ambiguous_devices_fail_closed(self):
        for status in (b"", bytes(16), b"\x02" + OFF[1:], OFF[:4] + b"\xff" + OFF[5:]):
            device = FakeLight(status)
            with self.assertRaises(ValueError):
                lamp.handle_event(event(True, unread=1), opener=lambda: device)
            self.assertEqual(device.writes, [])
        self.assertIsNone(lamp.unique_path([]))
        self.assertEqual(lamp.unique_path(["one"]), "one")
        with self.assertRaises(RuntimeError):
            lamp.unique_path(["one", "two"])

    def test_identity_guards(self):
        valid = {"vendor": 0x17EF, "product_id": 0x616C, "revision": 0x0101,
                 "product": "Magic Bay Light", "manufacturer": "Lenovo",
                 "caps": (0xFF00, 1, 16, 0, 60, 2)}
        lamp.validate_identity(valid)
        for key, bad in (("vendor", 0), ("revision", 0x29), ("product", "Other"),
                         ("manufacturer", "Other"), ("caps", (0, 1, 16, 0, 60, 2))):
            with self.assertRaises(ValueError):
                lamp.validate_identity({**valid, key: bad})


if __name__ == "__main__":
    unittest.main()
