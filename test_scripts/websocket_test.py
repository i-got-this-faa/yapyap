#!/usr/bin/env python3
"""
YapYap WebSocket Test Client
Comprehensive testing tool for WebSocket connections with authentication
"""

import asyncio
import websockets
import json
import time
import sys
import requests
from typing import Optional, Dict, Any
import signal


class WebSocketTester:
    def __init__(self, host: str = "localhost", port: int = 8080):
        self.host = host
        self.port = port
        self.token: Optional[str] = None
        self.websocket = None
        self.running = False

        # Statistics
        self.stats = {
            "messages_sent": 0,
            "responses_received": 0,
            "heartbeats_sent": 0,
            "errors": 0,
            "connection_time": 0,
            "start_time": 0,
        }

    async def authenticate(
        self, username: str = "testuser", password: str = "testpass123"
    ) -> bool:
        """Authenticate and get JWT token"""
        try:
            print(f"🔐 Authenticating as {username}...")

            response = requests.post(
                f"http://{self.host}:{self.port}/api/v1/auth/login",
                json={"username": username, "password": password},
                timeout=5,
            )

            if response.status_code == 200:
                data = response.json()
                self.token = data["token"]
                print(f"✅ Authentication successful")
                print(f"   User ID: {data['user_id']}")
                print(f"   Token: {self.token[:20]}...")
                return True
            else:
                print(f"❌ Authentication failed: {response.status_code}")
                print(f"   Response: {response.text}")
                return False

        except Exception as e:
            print(f"❌ Authentication error: {e}")
            return False

    async def connect_websocket(self) -> bool:
        """Connect to WebSocket with authentication"""
        if not self.token:
            print("❌ No authentication token available")
            return False

        try:
            print("🔌 Connecting to WebSocket...")
            uri = f"ws://{self.host}:{self.port}/ws?token={self.token}"

            self.websocket = await websockets.connect(uri)
            self.stats["connection_time"] = time.time()
            print("✅ WebSocket connected successfully")
            return True

        except Exception as e:
            print(f"❌ WebSocket connection failed: {e}")
            self.stats["errors"] += 1
            return False

    async def send_message(self, message_type: str, data: Dict[Any, Any]) -> bool:
        """Send a message via WebSocket"""
        if not self.websocket:
            print("❌ WebSocket not connected")
            return False

        try:
            message = {"type": message_type, "data": data}

            await self.websocket.send(json.dumps(message))
            self.stats["messages_sent"] += 1

            if message_type == "0000":
                self.stats["heartbeats_sent"] += 1
                print(f"💓 Sent heartbeat #{self.stats['heartbeats_sent']}")
            else:
                print(f"📤 Sent {message_type}: {data}")

            return True

        except Exception as e:
            print(f"❌ Failed to send message: {e}")
            self.stats["errors"] += 1
            return False

    async def listen_for_messages(self):
        """Listen for incoming WebSocket messages"""
        if not self.websocket:
            return

        try:
            async for message in self.websocket:
                try:
                    data = json.loads(message)
                    self.stats["responses_received"] += 1

                    if data.get("type") == "0000":
                        print(
                            f"💚 Received heartbeat response: {data.get('data', {}).get('timestamp', 'no timestamp')}"
                        )
                    else:
                        print(f"📥 Received: {data}")

                except json.JSONDecodeError:
                    print(f"⚠️  Received non-JSON message: {message}")

        except websockets.exceptions.ConnectionClosed:
            print("🔌 WebSocket connection closed")
        except Exception as e:
            print(f"❌ Error receiving messages: {e}")
            self.stats["errors"] += 1

    async def send_heartbeat(self):
        """Send a heartbeat message"""
        return await self.send_message(
            "0000",
            {
                "timestamp": int(time.time()),
                "heartbeat_id": self.stats["heartbeats_sent"] + 1,
            },
        )

    async def send_test_message(self, content: str):
        """Send a test chat message"""
        return await self.send_message(
            "3000", {"content": content, "channel_id": 1, "timestamp": int(time.time())}
        )

    def print_stats(self):
        """Print current statistics"""
        uptime = (
            int(time.time() - self.stats["start_time"])
            if self.stats["start_time"]
            else 0
        )
        connection_duration = (
            int(time.time() - self.stats["connection_time"])
            if self.stats["connection_time"]
            else 0
        )

        print("\n📊 Test Statistics:")
        print(f"   Test Duration: {uptime}s")
        print(f"   Connection Duration: {connection_duration}s")
        print(f"   Messages Sent: {self.stats['messages_sent']}")
        print(f"   Heartbeats Sent: {self.stats['heartbeats_sent']}")
        print(f"   Responses Received: {self.stats['responses_received']}")
        print(f"   Errors: {self.stats['errors']}")

        if self.stats["messages_sent"] > 0:
            response_rate = (
                self.stats["responses_received"] / self.stats["messages_sent"]
            ) * 100
            print(f"   Response Rate: {response_rate:.1f}%")

    async def run_test(self, duration: int = 30, heartbeat_interval: int = 5):
        """Run the WebSocket test for specified duration"""
        print(f"\n🚀 Starting WebSocket test for {duration} seconds")
        print(f"   Heartbeat interval: {heartbeat_interval} seconds")

        self.stats["start_time"] = time.time()
        self.running = True

        # Connect to WebSocket
        if not await self.connect_websocket():
            return False

        # Start listening for messages
        listen_task = asyncio.create_task(self.listen_for_messages())

        try:
            # Send initial heartbeat
            await self.send_heartbeat()

            # Send periodic messages
            start_time = time.time()
            next_heartbeat = start_time + heartbeat_interval
            next_message = start_time + 10  # Send test message every 10 seconds
            message_count = 0

            while self.running and (time.time() - start_time) < duration:
                current_time = time.time()

                # Send heartbeat
                if current_time >= next_heartbeat:
                    await self.send_heartbeat()
                    next_heartbeat = current_time + heartbeat_interval

                # Send test message
                if current_time >= next_message:
                    message_count += 1
                    await self.send_test_message(
                        f"Test message #{message_count} at {time.strftime('%H:%M:%S')}"
                    )
                    next_message = current_time + 10

                await asyncio.sleep(0.1)  # Small delay to prevent busy waiting

            print(f"\n⏰ Test duration completed ({duration}s)")

        except KeyboardInterrupt:
            print("\n⏹️  Test interrupted by user")
        except Exception as e:
            print(f"\n❌ Test error: {e}")
            self.stats["errors"] += 1
        finally:
            self.running = False
            listen_task.cancel()

            if self.websocket:
                await self.websocket.close()

            self.print_stats()

            # Determine test result
            if self.stats["errors"] == 0 and self.stats["responses_received"] > 0:
                print(
                    "\n✅ WebSocket test PASSED - Connection stable with active communication"
                )
                return True
            elif self.stats["responses_received"] > 0:
                print(
                    "\n⚠️  WebSocket test PARTIAL - Some communication but with errors"
                )
                return False
            else:
                print("\n❌ WebSocket test FAILED - No responses received")
                return False


def signal_handler(signum, frame):
    """Handle Ctrl+C gracefully"""
    print("\n\n⏹️  Test interrupted by user")
    sys.exit(0)


async def main():
    """Main test function"""
    print("YapYap WebSocket Comprehensive Test")
    print("===================================")

    # Parse command line arguments
    duration = int(sys.argv[1]) if len(sys.argv) > 1 else 30
    heartbeat_interval = int(sys.argv[2]) if len(sys.argv) > 2 else 5
    username = sys.argv[3] if len(sys.argv) > 3 else "testuser"
    password = sys.argv[4] if len(sys.argv) > 4 else "testpass123"

    # Setup signal handler
    signal.signal(signal.SIGINT, signal_handler)

    # Check if server is running
    print("1. Checking if server is running...")
    try:
        response = requests.get("http://localhost:8080/health", timeout=5)
        if response.status_code == 200:
            print("✅ Server is running")
        else:
            print(f"❌ Server health check failed: {response.status_code}")
            return
    except Exception as e:
        print(f"❌ Server is not running: {e}")
        print("Please start the server with: go run main.go")
        return

    # Run the test
    tester = WebSocketTester()

    # Authenticate
    if not await tester.authenticate(username, password):
        return

    # Run WebSocket test
    success = await tester.run_test(duration, heartbeat_interval)

    if success:
        print("\nFor more detailed testing, use the web interface:")
        print("  http://localhost:8080/static/websocket_comprehensive_test.html")

    return success


if __name__ == "__main__":
    try:
        result = asyncio.run(main())
        sys.exit(0 if result else 1)
    except KeyboardInterrupt:
        print("\n⏹️  Test interrupted")
        sys.exit(1)
    except Exception as e:
        print(f"\n❌ Unexpected error: {e}")
        sys.exit(1)
