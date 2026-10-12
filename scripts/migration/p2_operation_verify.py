"""Development-only P2 RPC check against an isolated Go daemon binary.

Uses generated Python gRPC bindings and a separate existing WS test interpreter.
Creates only owned temporary processes, random loopback ports, and a private DB.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import secrets
import socket
import sqlite3
import subprocess
import sys
from pathlib import Path


def seed_legacy(args: argparse.Namespace) -> None:
    from websockets.sync.client import connect

    with connect(args.endpoint, open_timeout=5) as connection:
        connection.send(json.dumps({"action": "daemon.handshake", "secret": args.secret}))
        assert json.loads(connection.recv(timeout=5))["action"] == "daemon.handshake.result"
        connection.send(json.dumps({
            "action": "session.rename", "agent_type": "fixture",
            "session_id": "fixture-session", "operation_id": "uncertain",
            "params": {"title": "fixture"},
        }))
        response = json.loads(connection.recv(timeout=5))
        assert response["action"] == "error" and response["operation_state"] == "uncertain"
        print(json.dumps({"operation_state": response["operation_state"]}))


def reserve_address() -> str:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return f"127.0.0.1:{listener.getsockname()[1]}"


def check(args: argparse.Namespace) -> None:
    sys.path.insert(0, str(args.root / "contracts/gen/python"))
    import grpc
    from astrorder.sessiond.v1 import sessiond_pb2 as pb
    from astrorder.sessiond.v1 import sessiond_pb2_grpc as rpc

    args.output.mkdir(parents=True, exist_ok=False)
    database = args.output / "private-daemon.db"
    grpc_secret, ws_secret = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
    metadata = (("authorization", "Bearer " + grpc_secret),)
    commands: list[str] = []
    assertions: list[str] = []
    process = None
    channel = None
    addresses: list[str] = []


    def start() -> tuple[object, object, object, str]:
        nonlocal process, channel
        target, legacy = reserve_address(), reserve_address()
        while target == legacy:
            legacy = reserve_address()
        addresses.extend((target, legacy))
        environment = {key: value for key, value in os.environ.items()
                       if not key.upper().startswith("ASTRORDER_")}
        environment.update({
            "ASTRORDER_SESSION_DAEMON_SECRET": ws_secret,
            "ASTRORDER_SESSION_DAEMON_DB": str(database),
            "ASTRORDER_SESSION_DAEMON_PORT": legacy.rsplit(":", 1)[1],
            "ASTRORDER_SESSION_DAEMON_GRPC_TARGET": target,
            "ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL": grpc_secret,
        })
        process = subprocess.Popen([str(args.daemon)], env=environment,
                                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                   stderr=subprocess.DEVNULL)
        channel = grpc.insecure_channel(target, options=(
            ("grpc.max_send_message_length", 33554432),
            ("grpc.max_receive_message_length", 33554432),
        ))
        grpc.channel_ready_future(channel).result(timeout=10)
        control = rpc.DaemonControlStub(channel)
        query = rpc.OperationQueryStub(channel)
        capability = control.GetCapabilities(pb.GetCapabilitiesRequest(), metadata=metadata, timeout=5)
        assert capability.operation_lookup
        assert list(capability.services) == ["astrorder.sessiond.v1.DaemonControl",
                                             "astrorder.sessiond.v1.EventStream",
                                             "astrorder.sessiond.v1.OperationQuery"]
        return control, query, capability, legacy

    def lookup(query: object, id_: str, action: str, *, fixture_scope: bool = False):
        key = pb.OperationKey(operation_id=id_, legacy_action=action,
                              canonicalization=pb.CANONICALIZATION_VERSION_LEGACY_JSON_V1)
        if fixture_scope:
            key.agent_type_compat.value = "fixture"
            key.session_id.value = "fixture-session"
        return query.GetOperation(pb.GetOperationRequest(key=key), metadata=metadata, timeout=5)

    try:
        control, query, capability, legacy = start()
        commands.append("isolated daemon start: explicit loopback WS and gRPC, private DB")
        assert lookup(query, "missing", "session.create").state == pb.OPERATION_STATE_MISSING
        assertions.append("default capabilities and operation query reachable from generated Python client")
        try:
            query.GetOperation(pb.GetOperationRequest(key=pb.OperationKey(
                operation_id="missing", legacy_action="session.create",
                canonicalization=pb.CANONICALIZATION_VERSION_LEGACY_JSON_V1)), timeout=5)
        except grpc.RpcError as error:
            assert error.code() == grpc.StatusCode.UNAUTHENTICATED
        else:
            raise AssertionError("query accepted missing authentication")
        assertions.append("missing metadata authentication refused")
        seeded = subprocess.run([str(args.ws_python), str(Path(__file__).resolve()),
                                 "seed", "--endpoint", "ws://" + legacy, "--secret", ws_secret],
                                stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=10, check=False)
        assert seeded.returncode == 0, "private WS fixture failed"
        assert json.loads(seeded.stdout)["operation_state"] == "uncertain"
        uncertain = lookup(query, "uncertain", "session.rename", fixture_scope=True)
        assert uncertain.state == pb.OPERATION_STATE_UNCERTAIN and not uncertain.HasField("typed_response")
        assertions.append("legacy WS-created uncertain reservation read via Python gRPC without retry")
        # Pending uncertainty requires explicit consent for this owned fixture only.
        response = control.Shutdown(pb.ShutdownRequest(expected_daemon_id=capability.daemon_id, confirm_active=True),
                                    metadata=metadata, timeout=5)
        assert response.accepted and process.wait(timeout=5) == 0
        channel.close()
        process, channel = None, None
        old_body = (f'{{"action":"session.create.result","daemon_id":"{capability.daemon_id}",'
                    '"session_id":"native-fixture","result":{"status":"idle",'
                    '"unknown":9007199254740993123456789}}').encode()
        # Synthetic completed legacy receipt in this now-stopped private fixture.
        with sqlite3.connect(database) as connection:
            connection.execute("INSERT INTO operations(scope,id,digest,response) VALUES(?,?,?,?)",
                               ('["session.create",null,null,null]', "completed", "fixture-digest", old_body))
            mismatched_body = old_body.replace(capability.daemon_id.encode(), b"different-daemon-fixture")
            connection.execute("INSERT INTO operations(scope,id,digest,response) VALUES(?,?,?,?)",
                               ('["session.create",null,null,null]', "different-daemon", "fixture-digest", mismatched_body))
            malformed_body = b'{"unterminated"'
            connection.execute("INSERT INTO operations(scope,id,digest,response) VALUES(?,?,?,?)",
                               ('["session.create",null,null,null]', "malformed", "fixture-digest", malformed_body))
        control, query, reopened, _ = start()
        # Existing journal identity survives reopening the same private DB.
        assert reopened.daemon_id == capability.daemon_id
        receipt = lookup(query, "completed", "session.create")
        assert receipt.state == pb.OPERATION_STATE_COMPLETED
        assert receipt.legacy_response_json == old_body and receipt.HasField("typed_response")
        assert not receipt.HasField("request_sha256")
        mismatched = lookup(query, "different-daemon", "session.create")
        assert mismatched.legacy_response_json == mismatched_body and not mismatched.HasField("typed_response")
        malformed = lookup(query, "malformed", "session.create")
        assert malformed.state == pb.OPERATION_STATE_COMPLETED
        assert malformed.legacy_response_json == malformed_body and not malformed.HasField("typed_response")
        assert lookup(query, "uncertain", "session.rename", fixture_scope=True).state == pb.OPERATION_STATE_UNCERTAIN
        assertions.append("private DB reopen preserved completed exact bytes, big integer, and uncertain reservation")
        assertions.append("persisted journal identity survives reopen; a mismatched fixture identity produces no synthetic typed receipt")
        assertions.append("malformed completed bytes remain available with no fabricated typed receipt")
        with sqlite3.connect(database) as connection:
            assert connection.execute("SELECT count(*) FROM operations").fetchone()[0] == 4
        assertions.append("lookups added no operation rows")
        response = control.Shutdown(pb.ShutdownRequest(expected_daemon_id=reopened.daemon_id, confirm_active=True),
                                    metadata=metadata, timeout=5)
        assert response.accepted and process.wait(timeout=5) == 0
        channel.close()
        process, channel = None, None
        for address in addresses:
            host, port = address.rsplit(":", 1)
            with socket.socket() as probe:
                probe.settimeout(1)
                assert probe.connect_ex((host, int(port))) != 0, "owned listener survived shutdown"
        assertions.append("both exact owned processes exited and all WS/gRPC listener ports released")
        result = {"schema_version": 1, "passed": True, "stage": "P2-partial",
                  "fixture_receipt": "synthetic completed; real legacy WS uncertain",
                  "daemon_sha256": hashlib.sha256(args.daemon.read_bytes()).hexdigest(),
                  "commands": commands, "assertions": assertions, "cleanup": "owned processes exited; ports released",
                  "production_changed": False, "full_migration_complete": False}
        (args.output / "result.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(result))
    finally:
        if channel is not None:
            channel.close()
        if process is not None and process.poll() is None:
            process.kill()
            process.wait(timeout=5)


def main() -> None:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    seed = subparsers.add_parser("seed")
    seed.add_argument("--endpoint", required=True)
    seed.add_argument("--secret", required=True)
    verify = subparsers.add_parser("verify")
    verify.add_argument("--root", type=Path, required=True)
    verify.add_argument("--daemon", type=Path, required=True)
    verify.add_argument("--ws-python", type=Path, required=True)
    verify.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "seed":
        seed_legacy(args)
    else:
        check(args)


if __name__ == "__main__":
    main()
