import socket
import hmac
import hashlib
import time

HOST = "127.0.0.1"
PORT = 5667
PASSWORD = "CHANGE_ME"

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(3)

try:
    print("[1] Envoi HELLO...")
    s.sendto(b"HELLO", (HOST, PORT))

    data, addr = s.recvfrom(4096)
    response = data.decode()

    print("[2] RESPONSE :", response)
    print("[3] SOURCE   :", addr)

    if not response.startswith("CHALLENGE "):
        raise RuntimeError("Challenge inattendu")

    challenge_hex = response[len("CHALLENGE "):]
    nonce = bytes.fromhex(challenge_hex)

    print("[4] CHALLENGE :", challenge_hex)

    # Exactement comme auth.go :
    # hmac.New(sha256.New, []byte(password))
    # mac.Write(entry.nonce)
    auth = hmac.new(
        PASSWORD.encode(),
        nonce,
        hashlib.sha256
    ).hexdigest()

    print("[5] AUTH :", auth)
    print("[6] Envoi AUTH...")

    s.sendto(("AUTH " + auth).encode(), (HOST, PORT))

    data, addr = s.recvfrom(4096)
    result = data.decode()

    print("[7] RESPONSE :", result)
    print("[8] SOURCE   :", addr)

    if result == "AUTH_OK":
        print()
        print("================================")
        print(" AUTHENTIFICATION + SESSION OK")
        print("================================")
    else:
        print()
        print("================================")
        print(" AUTHENTIFICATION REFUSEE")
        print("================================")

finally:
    s.close()
