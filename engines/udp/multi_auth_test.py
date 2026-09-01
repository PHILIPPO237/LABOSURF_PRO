import socket
import hmac
import hashlib

HOST = "127.0.0.1"
PORT = 5667
PASSWORD = "CHANGE_ME"

def test(numero):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.settimeout(3)

    print(f"\n===== TEST {numero} =====")
    s.sendto(b"HELLO", (HOST, PORT))

    data, _ = s.recvfrom(4096)
    text = data.decode()

    if not text.startswith("CHALLENGE "):
        print("ERREUR CHALLENGE :", text)
        s.close()
        return False

    challenge = text.split(" ", 1)[1]

    auth = hmac.new(
        PASSWORD.encode(),
        bytes.fromhex(challenge),
        hashlib.sha256
    ).hexdigest()

    s.sendto(("AUTH " + auth).encode(), (HOST, PORT))

    data, addr = s.recvfrom(4096)
    response = data.decode()

    print("REPONSE :", response)
    print("SOURCE  :", addr)

    s.close()
    return response == "AUTH_OK"

ok = 0

for i in range(1, 4):
    if test(i):
        ok += 1

print("\n==============================")
print(f"RESULTAT : {ok}/3 authentifications réussies")
print("==============================")
