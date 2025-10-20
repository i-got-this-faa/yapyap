#!/usr/bin/env python3
"""
WebSocket + REST integration test for YapYap.

Features:
- Auth (login with register fallback)
- Connect WS with JWT token and print incoming events
- Optional heartbeat (ping frame)

Args:
  [duration] [heartbeat_interval] [username] [password] [host] [port]
Defaults:
  duration=30, heartbeat_interval=30, username=testuser, password=testpass123, host=localhost, port=8080
"""

import asyncio
import websockets
import logging
import signal
import sys
import time
import requests

logging.basicConfig(level=logging.INFO)


class WebSocketClient:
    def __init__(self, uri, heartbeat_interval=30):
        self.uri = uri
        self.heartbeat_interval = heartbeat_interval
        self.keep_running = True

    async def send_heartbeat(self, websocket):
        while self.keep_running:
            await asyncio.sleep(self.heartbeat_interval)
            try:
                # Send a ping frame (websockets auto-handles pong)
                await websocket.ping()
                logging.info("Sent heartbeat ping frame")
            except Exception as e:
                logging.error(f"Error sending heartbeat: {e}")
                self.keep_running = False

    async def receive_messages(self, websocket):
        try:
            async for message in websocket:
                logging.info(f"Received message: {message}")
        except websockets.ConnectionClosed:
            logging.info("Connection closed")
            self.keep_running = False

    async def run(self):
        async with websockets.connect(self.uri) as websocket:
            heartbeat_task = asyncio.create_task(self.send_heartbeat(websocket))
            receive_task = asyncio.create_task(self.receive_messages(websocket))
            await asyncio.wait([heartbeat_task, receive_task], return_when=asyncio.FIRST_COMPLETED)

    def stop(self):
        self.keep_running = False


def authenticate(host: str, port: int, username: str, password: str) -> str:
    """Login; if fails, register then login. Returns JWT token or empty string."""
    try:
        r = requests.post(
            f"http://{host}:{port}/api/v1/auth/login",
            json={"username": username, "password": password},
            timeout=10,
        )
        if r.status_code == 200:
            return r.json().get("token", "")
        # Try to register then login again
        requests.post(
            f"http://{host}:{port}/api/v1/auth/register",
            json={"username": username, "password": password},
            timeout=10,
        )
        r = requests.post(
            f"http://{host}:{port}/api/v1/auth/login",
            json={"username": username, "password": password},
            timeout=10,
        )
        if r.status_code == 200:
            return r.json().get("token", "")
        logging.error("Login failed: %s %s", r.status_code, r.text)
        return ""
    except Exception as e:
        logging.error("Auth error: %s", e)
        return ""


def main():
    # Parse args
    duration = int(sys.argv[1]) if len(sys.argv) > 1 else 30
    heartbeat_interval = int(sys.argv[2]) if len(sys.argv) > 2 else 30
    username = sys.argv[3] if len(sys.argv) > 3 else "testuser"
    password = sys.argv[4] if len(sys.argv) > 4 else "testpass123"
    host = sys.argv[5] if len(sys.argv) > 5 else "localhost"
    port = int(sys.argv[6]) if len(sys.argv) > 6 else 8080

    # Health check
    try:
        health = requests.get(f"http://{host}:{port}/health", timeout=5)
        if health.status_code != 200:
            logging.error("Health check failed: %s", health.status_code)
            sys.exit(1)
        logging.info("Server health OK")
    except Exception as e:
        logging.error("Health check error: %s", e)
        sys.exit(1)

    # Auth and build WS URL
    token = authenticate(host, port, username, password)
    if not token:
        logging.error("Could not obtain JWT token")
        sys.exit(1)
    uri = f"ws://{host}:{port}/ws?token={token}"
    logging.info("Connecting to %s", uri)

    client = WebSocketClient(uri, heartbeat_interval=heartbeat_interval)

    # Graceful shutdown
    def handle_sigint(signum, frame):
        logging.info("Interrupted, stopping…")
        client.stop()

    signal.signal(signal.SIGINT, handle_sigint)

    async def runner():
        try:
            await asyncio.wait_for(client.run(), timeout=duration)
        except asyncio.TimeoutError:
            logging.info("Finished duration (%ss)", duration)
            client.stop()

    asyncio.run(runner())


if __name__ == "__main__":
    main()

