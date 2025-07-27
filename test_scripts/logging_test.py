#!/usr/bin/env python3
"""
Test script for YapYap logging system.
This script demonstrates how to interact with the new logging endpoints.
"""

import requests
import json
import time
from datetime import datetime

# Configuration
BASE_URL = "http://localhost:8080/api/v1"


def login(username, password):
    """Login and get JWT token"""
    response = requests.post(
        f"{BASE_URL}/auth/login", json={"username": username, "password": password}
    )
    if response.status_code == 200:
        return response.json()["token"]
    else:
        print(f"Login failed: {response.text}")
        return None


def get_logs(token, **filters):
    """Get logs with optional filters"""
    headers = {"Authorization": f"Bearer {token}"}
    params = {k: v for k, v in filters.items() if v is not None}

    response = requests.get(f"{BASE_URL}/logs", headers=headers, params=params)
    if response.status_code == 200:
        return response.json()
    else:
        print(f"Failed to get logs: {response.text}")
        return None


def get_log_stats(token):
    """Get log statistics"""
    headers = {"Authorization": f"Bearer {token}"}

    response = requests.get(f"{BASE_URL}/logs/stats", headers=headers)
    if response.status_code == 200:
        return response.json()
    else:
        print(f"Failed to get log stats: {response.text}")
        return None


def create_channel(token, name, channel_type=0):
    """Create a test channel to generate log entries"""
    headers = {"Authorization": f"Bearer {token}"}

    response = requests.post(
        f"{BASE_URL}/channels",
        headers=headers,
        json={"name": name, "type": channel_type},
    )
    if response.status_code == 201:
        return response.json()
    else:
        print(f"Failed to create channel: {response.text}")
        return None


def send_message(token, channel_id, content):
    """Send a test message to generate log entries"""
    headers = {"Authorization": f"Bearer {token}"}

    response = requests.post(
        f"{BASE_URL}/messages",
        headers=headers,
        json={"channel_id": channel_id, "content": content},
    )
    if response.status_code == 201:
        return response.json()
    else:
        print(f"Failed to send message: {response.text}")
        return None


def main():
    print("YapYap Logging System Test")
    print("=" * 40)

    # Get credentials
    username = input("Username: ").strip()
    password = input("Password: ").strip()

    # Login
    print("\n1. Logging in...")
    token = login(username, password)
    if not token:
        return
    print("✓ Login successful")

    # Create some test data to generate logs
    print("\n2. Creating test channel...")
    channel = create_channel(token, f"test-logging-{int(time.time())}")
    if channel:
        print(f"✓ Channel created: {channel['name']}")

        print("\n3. Sending test message...")
        message = send_message(
            token, channel["id"], "This is a test message for logging!"
        )
        if message:
            print("✓ Message sent")

    # Wait a moment for logs to be written
    time.sleep(1)

    # Get log statistics
    print("\n4. Getting log statistics...")
    stats = get_log_stats(token)
    if stats:
        print(f"✓ Total logs: {stats['total_logs']}")
        print("  Logs by level:")
        for level, count in stats["logs_by_level"].items():
            print(f"    {level}: {count}")
        print("  Recent actions:")
        for log in stats["recent_actions"][:5]:  # Show last 5
            timestamp = log["created_at"][:19].replace("T", " ")
            print(
                f"    {timestamp} - {log['level']} - {log['action']}: {log['message'][:50]}..."
            )

    # Get recent logs
    print("\n5. Getting recent logs...")
    logs = get_logs(token, limit=10)
    if logs:
        print(f"✓ Retrieved {len(logs['logs'])} logs (of {logs['total_count']} total)")
        for log in logs["logs"][:5]:  # Show first 5
            timestamp = log["created_at"][:19].replace("T", " ")
            user = (
                log.get("user", {}).get("username", "system")
                if log.get("user")
                else "system"
            )
            print(
                f"    {timestamp} - {log['action']} by {user}: {log['message'][:60]}..."
            )

    # Filter by action type
    print("\n6. Getting message-related logs...")
    msg_logs = get_logs(token, action="message.send", limit=5)
    if msg_logs and msg_logs["logs"]:
        print(f"✓ Found {len(msg_logs['logs'])} message logs")
        for log in msg_logs["logs"]:
            timestamp = log["created_at"][:19].replace("T", " ")
            user = (
                log.get("user", {}).get("username", "unknown")
                if log.get("user")
                else "unknown"
            )
            print(f"    {timestamp} - Message sent by {user}")
    else:
        print("  No message logs found")

    # Filter by level
    print("\n7. Getting error logs...")
    error_logs = get_logs(token, level=3, limit=5)  # LogLevelError = 3
    if error_logs and error_logs["logs"]:
        print(f"✓ Found {len(error_logs['logs'])} error logs")
        for log in error_logs["logs"]:
            timestamp = log["created_at"][:19].replace("T", " ")
            print(f"    {timestamp} - ERROR: {log['message'][:60]}...")
    else:
        print("  No error logs found")

    print("\n" + "=" * 40)
    print("Logging system test completed!")
    print("\nAPI Endpoints:")
    print(f"  GET {BASE_URL}/logs - Get logs with filters")
    print(f"  GET {BASE_URL}/logs/stats - Get log statistics")
    print("\nFilters for /logs endpoint:")
    print("  - level: 0=DEBUG, 1=INFO, 2=WARN, 3=ERROR, 4=FATAL")
    print("  - action: e.g., 'message.send', 'channel.create', 'user.login'")
    print("  - user_id: Filter by user ID")
    print("  - start_date/end_date: ISO 8601 format")
    print("  - limit/offset: Pagination")


if __name__ == "__main__":
    main()
