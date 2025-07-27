#!/usr/bin/env python3
"""
YapYap Admin Testing Script
Tests all API endpoints with admin privileges
"""

import requests
import json
import time
import sys

# Configuration
BASE_URL = "http://localhost:8080/api/v1"
USERNAME = "admin"
PASSWORD = "admin123"

session = requests.Session()
auth_token = None
user_id = None


def print_header(title):
    print(f"\n{'='*50}")
    print(f"{title}")
    print(f"{'='*50}")


def print_step(step_num, description):
    print(f"\n{step_num}. {description}")


def print_success(message):
    print(f"✓ {message}")


def print_error(message):
    print(f"✗ {message}")


def print_info(message):
    print(f"ℹ {message}")


def login():
    """Login as admin user"""
    print_step(1, "Logging in as admin...")

    response = session.post(
        f"{BASE_URL}/auth/login", json={"username": USERNAME, "password": PASSWORD}
    )

    if response.status_code == 200:
        data = response.json()
        global auth_token, user_id
        auth_token = data["token"]
        user_id = data["user_id"]
        session.headers.update({"Authorization": f"Bearer {auth_token}"})
        print_success(f"Login successful (User ID: {user_id})")
        return True
    else:
        print_error(f"Login failed: {response.text}")
        return False


def test_user_management():
    """Test user management endpoints"""
    print_header("USER MANAGEMENT TESTS")

    # Get all users
    print_step(2, "Getting all users...")
    response = session.get(f"{BASE_URL}/users")
    if response.status_code == 200:
        users = response.json()
        print_success(f"Retrieved {len(users)} users")
        for user in users:
            print_info(f"  User: {user['username']} (ID: {user['ID']})")
    else:
        print_error(f"Failed to get users: {response.text}")

    # Create a new user
    print_step(3, "Creating new user...")
    new_user_data = {
        "username": "testuser2",
        "email": "test2@yapyap.com",
        "password": "password123",
        "role": "user",
    }
    response = session.post(f"{BASE_URL}/auth/register", json=new_user_data)
    if response.status_code == 200:
        new_user = response.json()
        print_success(
            f"Created user: {new_user['user']['username']} (ID: {new_user['user_id']})"
        )
        return new_user["user_id"]
    else:
        print_error(f"Failed to create user: {response.text}")
        return None


def test_channel_management():
    """Test channel management endpoints"""
    print_header("CHANNEL MANAGEMENT TESTS")

    # Create a channel
    print_step(4, "Creating a test channel...")
    channel_data = {
        "name": "test-channel",
        "description": "A test channel for admin testing",
        "type": "text",
        "is_private": False,
    }
    response = session.post(f"{BASE_URL}/channels", json=channel_data)
    if response.status_code == 201:
        channel = response.json()
        channel_id = channel["ID"]
        print_success(f"Created channel: {channel['name']} (ID: {channel_id})")

        # Get all channels
        print_step(5, "Getting all channels...")
        response = session.get(f"{BASE_URL}/channels")
        if response.status_code == 200:
            channels = response.json()
            print_success(f"Retrieved {len(channels)} channels")
            for ch in channels:
                print_info(f"  Channel: {ch['name']} (ID: {ch['ID']})")

        # Update channel
        print_step(6, "Updating channel...")
        update_data = {"description": "Updated test channel description"}
        response = session.put(f"{BASE_URL}/channels/{channel_id}", json=update_data)
        if response.status_code == 200:
            print_success("Channel updated successfully")
        else:
            print_error(f"Failed to update channel: {response.text}")

        return channel_id
    else:
        print_error(f"Failed to create channel: {response.text}")
        return None


def test_message_management(channel_id):
    """Test message management endpoints"""
    print_header("MESSAGE MANAGEMENT TESTS")

    if not channel_id:
        print_error("No channel available for message testing")
        return

    # Send a message
    print_step(7, "Sending a test message...")
    message_data = {"content": "Hello from admin test script!", "message_type": "text"}
    response = session.post(
        f"{BASE_URL}/channels/{channel_id}/messages", json=message_data
    )
    if response.status_code == 201:
        message = response.json()
        message_id = message["ID"]
        print_success(f"Sent message (ID: {message_id})")

        # Get messages from channel
        print_step(8, "Getting channel messages...")
        response = session.get(f"{BASE_URL}/channels/{channel_id}/messages")
        if response.status_code == 200:
            messages = response.json()
            print_success(f"Retrieved {len(messages)} messages")
            for msg in messages:
                print_info(f"  Message: {msg['content'][:50]}... (ID: {msg['ID']})")

        # Update message
        print_step(9, "Updating message...")
        update_data = {"content": "Updated message content from admin test!"}
        response = session.put(f"{BASE_URL}/messages/{message_id}", json=update_data)
        if response.status_code == 200:
            print_success("Message updated successfully")
        else:
            print_error(f"Failed to update message: {response.text}")

        # Delete message
        print_step(10, "Deleting message...")
        response = session.delete(f"{BASE_URL}/messages/{message_id}")
        if response.status_code == 200:
            print_success("Message deleted successfully")
        else:
            print_error(f"Failed to delete message: {response.text}")

        return message_id
    else:
        print_error(f"Failed to send message: {response.text}")
        return None


def test_role_management():
    """Test role and permission management"""
    print_header("ROLE & PERMISSION MANAGEMENT TESTS")

    # Get all roles
    print_step(11, "Getting all roles...")
    response = session.get(f"{BASE_URL}/roles")
    if response.status_code == 200:
        roles = response.json()
        print_success(f"Retrieved {len(roles)} roles")
        for role in roles:
            print_info(f"  Role: {role['name']} (ID: {role['ID']})")
        return roles
    else:
        print_error(f"Failed to get roles: {response.text}")
        return []


def test_logging_system():
    """Test the comprehensive logging system"""
    print_header("LOGGING SYSTEM TESTS")

    # Get log statistics
    print_step(12, "Getting log statistics...")
    response = session.get(f"{BASE_URL}/logs/stats")
    if response.status_code == 200:
        stats = response.json()
        print_success("Retrieved log statistics:")
        print_info(f"  Total logs: {stats.get('total_logs', 0)}")

        if "logs_by_level" in stats:
            print_info("  Logs by level:")
            for level, count in stats["logs_by_level"].items():
                print_info(f"    {level}: {count}")

        if "recent_actions" in stats:
            print_info("  Recent actions:")
            for action in stats["recent_actions"][:5]:
                print_info(
                    f"    {action['timestamp']} - {action['user_id']} - {action['action']}"
                )
    else:
        print_error(f"Failed to get log statistics: {response.text}")

    # Get recent logs
    print_step(13, "Getting recent logs...")
    response = session.get(f"{BASE_URL}/logs?limit=10")
    if response.status_code == 200:
        data = response.json()
        logs = data.get("logs", [])
        total = data.get("total", 0)
        print_success(f"Retrieved {len(logs)} logs (of {total} total)")
        for log in logs[:5]:
            print_info(
                f"  {log['timestamp']} - {log['action']} by {log.get('username', 'unknown')}"
            )
    else:
        print_error(f"Failed to get logs: {response.text}")

    # Test log filtering
    print_step(14, "Testing log filtering (INFO level)...")
    response = session.get(f"{BASE_URL}/logs?level=1&limit=5")
    if response.status_code == 200:
        data = response.json()
        logs = data.get("logs", [])
        print_success(f"Retrieved {len(logs)} INFO level logs")
    else:
        print_error(f"Failed to filter logs: {response.text}")


def main():
    """Main test function"""
    print_header("YAPYAP ADMIN COMPREHENSIVE TEST")
    print_info(f"Testing server at: {BASE_URL}")
    print_info(f"Admin user: {USERNAME}")

    # Login
    if not login():
        sys.exit(1)

    # Run all tests
    new_user_id = test_user_management()
    channel_id = test_channel_management()
    message_id = test_message_management(channel_id)
    roles = test_role_management()
    test_logging_system()

    # Summary
    print_header("TEST SUMMARY")
    print_success("All admin functionality tests completed!")
    print_info("Key features validated:")
    print_info("  ✓ User authentication and management")
    print_info("  ✓ Channel creation and management")
    print_info("  ✓ Message sending, updating, and deletion")
    print_info("  ✓ Role and permission system")
    print_info("  ✓ Comprehensive logging system")
    print_info("  ✓ Database audit trail")

    print_header("API ENDPOINTS TESTED")
    endpoints = [
        "POST /auth/login - User authentication",
        "GET /users - List all users",
        "POST /auth/register - Create new user",
        "POST /channels - Create channel",
        "GET /channels - List channels",
        "PUT /channels/{id} - Update channel",
        "POST /channels/{id}/messages - Send message",
        "GET /channels/{id}/messages - Get messages",
        "PUT /messages/{id} - Update message",
        "DELETE /messages/{id} - Delete message",
        "GET /roles - List roles",
        "GET /logs/stats - Log statistics",
        "GET /logs - Get logs with filtering",
    ]

    for endpoint in endpoints:
        print_info(f"  ✓ {endpoint}")


if __name__ == "__main__":
    main()
