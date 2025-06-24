import requests
import time
import json

API_URL = "http://localhost:8080/api/v1"


# Test user registration and login
def register_and_login(username, password):
    r = requests.post(
        f"{API_URL}/auth/register", json={"username": username, "password": password}
    )
    if r.status_code == 201:
        print(f"Registered user: {username}")
    elif r.status_code == 409:
        print(f"User {username} already exists")
    else:
        print(f"Registration error: {r.text}")
    r = requests.post(
        f"{API_URL}/auth/login", json={"username": username, "password": password}
    )
    if r.status_code == 200:
        token = r.json()["token"]
        print(f"Logged in as {username}")
        return token
    else:
        print(f"Login error: {r.text}")
        return None


def main():
    username = "cachetestuser"
    password = "cachetestpass"
    token = register_and_login(username, password)
    if not token:
        return
    print("\nNow test WebSocket message sending and permission cache manually.")
    print(
        "You can update permissions in the DB and observe cache behavior in the server logs."
    )
    print("Or extend this script to automate WebSocket tests.")


if __name__ == "__main__":
    main()
