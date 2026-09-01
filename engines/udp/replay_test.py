import socket
import hmac
import hashlib

HOST = "127.0.0.1"
PORT = 5667
PASSWORD = "CHANGE_ME"

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(3)

try:
    print("[1] HELLO...")
    s.sendto(b"HELLO", (HOST, PORT))

    data, addr = s.recvfrom(4096)
    response = data.decode()

    print("[2] RESPONSE :", response)

    challenge = response.split(" ", 1)[1]
    nonce = bytes.fromhex(challenge)

    auth = hmac.new(
        PASSWORD.encode(),
        nonce,
        hashlib.sha256
    ).hexdigest()

    print("[3] AUTH...")
    s.sendto(("AUTH " + auth).encode(), (HOST, PORT))

    data, _ = s.recvfrom(4096)
    print("[4] PREMIERE REPONSE :", data.decode())

    print("[5] REPLAY DU MEME AUTH...")
    s.sendto(("AUTH " + auth).encode(), (HOST, PORT))

    data, _ = s.recvfrom(4096)
    print("[6] DEUXIEME REPONSE :", data.decode())

finally:
    s.close()
