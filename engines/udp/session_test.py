import socket
import hmac
import hashlib

HOST = "127.0.0.1"
PORT = 5667
PASSWORD = "CHANGE_ME"

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(3)

print("[1] Envoi HELLO...")
s.sendto(b"HELLO", (HOST, PORT))

data, addr = s.recvfrom(4096)
print("[2] REPONSE :", data.decode())

if not data.startswith(b"CHALLENGE "):
    print("ERREUR : challenge invalide")
    s.close()
    raise SystemExit(1)

challenge = data.decode().split(" ", 1)[1]
print("[3] CHALLENGE :", challenge)

auth = hmac.new(
    PASSWORD.encode(),
    bytes.fromhex(challenge),
    hashlib.sha256
).hexdigest()

print("[4] AUTH :", auth)
print("[5] Envoi AUTH...")

s.sendto(("AUTH " + auth).encode(), (HOST, PORT))

data, addr = s.recvfrom(4096)
response = data.decode()

print("[6] REPONSE :", response)
print("[7] SOURCE :", addr)

if response == "AUTH_OK":
    print()
    print("================================")
    print(" AUTHENTIFICATION + SESSION OK")
    print("================================")
else:
    print()
    print("AUTHENTIFICATION ECHOUEE")

s.close()
