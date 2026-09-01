import socket
import hmac
import hashlib
import binascii

HOST = "127.0.0.1"
PORT = 5667
PASSWORD = "CHANGE_ME"

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(5)

try:
    print("[1] Envoi HELLO...")
    s.sendto(b"HELLO", (HOST, PORT))

    data, addr = s.recvfrom(4096)
    response = data.decode(errors="replace").strip()

    print("[2] REPONSE :", response)

    if not response.startswith("CHALLENGE "):
        print("ERREUR : CHALLENGE attendu")
        raise SystemExit(1)

    challenge_hex = response[len("CHALLENGE "):].strip()
    print("[3] CHALLENGE :", challenge_hex)

    nonce = binascii.unhexlify(challenge_hex)

    auth = hmac.new(
        PASSWORD.encode(),
        nonce,
        hashlib.sha256
    ).hexdigest()

    print("[4] AUTH :", auth)

    # Le serveur exige le préfixe "AUTH "
    packet = ("AUTH " + auth).encode()

    print("[5] Envoi AUTH...")
    s.sendto(packet, addr)

    data, addr = s.recvfrom(4096)
    response = data.decode(errors="replace").strip()

    print("[6] REPONSE :", response)

    if response == "AUTH_OK":
        print()
        print("================================")
        print(" AUTHENTIFICATION REUSSIE")
        print("================================")
    elif response == "AUTH_FAIL":
        print()
        print("AUTHENTIFICATION REFUSEE")
    else:
        print()
        print("REPONSE INATTENDUE :", response)

except socket.timeout:
    print()
    print("TIMEOUT : aucune reponse UDP")

except Exception as e:
    print()
    print("ERREUR :", e)

finally:
    s.close()
