import socket
import hmac
import hashlib

HOST = "127.0.0.1"
PORT = 5667

GOOD_PASSWORD = "CHANGE_ME"
BAD_PASSWORD = "MAUVAIS_MOT_DE_PASSE"

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(3)

print("[1] Envoi HELLO...")
s.sendto(b"HELLO", (HOST, PORT))

data, addr = s.recvfrom(4096)
print("[2] REPONSE :", data.decode())

challenge = data.decode().split(" ", 1)[1]
print("[3] CHALLENGE :", challenge)

auth = hmac.new(
    BAD_PASSWORD.encode(),
    bytes.fromhex(challenge),
    hashlib.sha256
).hexdigest()

print("[4] AUTH avec mauvais mot de passe :", auth)
print("[5] Envoi AUTH...")

s.sendto(("AUTH " + auth).encode(), (HOST, PORT))

try:
    data, addr = s.recvfrom(4096)
    print("[6] REPONSE :", data.decode())

    if data.decode() == "AUTH_FAIL":
        print()
        print("==============================")
        print(" AUTHENTIFICATION REFUSEE : OK")
        print("==============================")
    else:
        print()
        print("ERREUR : réponse inattendue")

except socket.timeout:
    print()
    print("TIMEOUT : aucune réponse")

s.close()
