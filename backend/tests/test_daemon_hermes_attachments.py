from __future__ import annotations

import base64
from pathlib import Path
from types import SimpleNamespace

import pytest

from astrorder.connections import ConnectionError
from astrorder.adapters.hermes.inputs import pack_daemon_attachments, stage_daemon_attachments


def test_pack_daemon_attachments_resolves_only_registered_blob_without_path(tmp_path: Path):
    blob = tmp_path / "stored.blob"
    blob.write_bytes(b"image-bytes")

    class Store:
        def get_attachment(self, attachment_id):
            assert attachment_id == "attachment-1"
            return {
                "id": attachment_id,
                "name": "proof.png",
                "media_type": "image/png",
                "size": len(b"image-bytes"),
                "storage_name": blob.name,
            }

    settings = SimpleNamespace(
        attachments_dir=tmp_path,
        max_attachment_size=1024,
        allowed_attachment_types=("image/png",),
    )
    payloads = pack_daemon_attachments(
        {"attachments": [{"id": "attachment-1"}]}, settings, Store()
    )

    assert payloads == [
        {
            "name": "proof.png",
            "media_type": "image/png",
            "content_base64": base64.b64encode(b"image-bytes").decode("ascii"),
        }
    ]
    assert "path" not in payloads[0]
    assert "storage_name" not in payloads[0]


def test_stage_daemon_attachments_validates_and_uses_native_byte_apis():
    calls: list[tuple[str, dict[str, object]]] = []

    def rpc(method, params, timeout=20):
        del timeout
        calls.append((method, params))
        if method == "image.attach_bytes":
            return {"result": {"attached": True, "path": "native-image"}}
        if method == "file.attach":
            return {"result": {"attached": True, "ref_text": "[file:notes.txt]"}}
        raise AssertionError(method)

    text, images = stage_daemon_attachments(
        rpc,
        "native-handle",
        "inspect both",
        [
            {
                "name": "proof.png",
                "media_type": "image/png",
                "content_base64": base64.b64encode(b"png").decode("ascii"),
            },
            {
                "name": "notes.txt",
                "media_type": "text/plain",
                "content_base64": base64.b64encode(b"notes").decode("ascii"),
            },
        ],
        maximum=1024,
        allowed_types=("image/png", "text/plain"),
    )

    assert text == "[file:notes.txt]\ninspect both"
    assert images == ["native-image"]
    assert calls[0][0] == "image.attach_bytes"
    assert calls[1][0] == "file.attach"
    assert "data:text/plain;base64," in str(calls[1][1]["data_url"])


def test_stage_daemon_attachments_rejects_invalid_base64_before_native_rpc():
    with pytest.raises(ConnectionError, match="编码"):
        stage_daemon_attachments(
            lambda *_args, **_kwargs: pytest.fail("native RPC must not run"),
            "native-handle",
            "prompt",
            [
                {
                    "name": "proof.png",
                    "media_type": "image/png",
                    "content_base64": "not-base64!",
                }
            ],
            maximum=1024,
            allowed_types=("image/png",),
        )
