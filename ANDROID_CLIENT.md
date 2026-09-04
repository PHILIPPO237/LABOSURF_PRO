# Client VPN Android — Guide d'implémentation

Ce document décrit comment implémenter un client VPN Android compatible
avec le protocole LABOSURF PRO (voir `PROTOCOL.md`).

## Pourquoi pas `/dev/net/tun` directement ?

Le fichier `tun_android.go` existant tente d'ouvrir `/dev/net/tun` et
d'appeler `ioctl(TUNSETIFF)`. **Cela ne fonctionne pas sur Android
non-rooté** : seules les applications système peuvent accéder à ce
périphérique.

Sur Android, l'unique méthode légitime pour créer un tunnel VPN est
l'API **`VpnService`** :

1. L'application déclare un service héritant de `android.net.VpnService`.
2. L'utilisateur accorde la permission VPN (dialogue système).
3. Le service appelle `Builder.establish()` qui retourne un
   `ParcelFileDescriptor` — c'est l'équivalent du fd TUN.
4. L'application lit/écrit des paquets IP bruts sur ce descripteur.

## Architecture recommandée

```
┌─────────────────────────────────────────────────┐
│              Application Android                 │
│                                                  │
│  ┌─────────────┐         ┌──────────────────┐   │
│  │  UI (Kotlin) │────────►│  VpnService       │   │
│  │  - serveur   │         │  (Kotlin/Java)    │   │
│  │  - user/mdp  │         │                   │   │
│  │  - connect   │         │  ┌─────────────┐  │   │
│  └─────────────┘         │  │ TUN fd      │  │   │
│                          │  │ (VpnService) │  │   │
│                          │  └──────┬──────┘  │   │
│                          │         │          │   │
│                          │  ┌──────▼──────┐  │   │
│                          │  │ UDP socket  │  │   │
│                          │  │ (protocole  │  │   │
│                          │  │  LABOSURF)  │  │   │
│                          │  └──────┬──────┘  │   │
│                          └─────────┼──────────┘   │
│                                    │              │
└────────────────────────────────────┼──────────────┘
                                     │
                              UDP → VPS:5667
```

## Implémentation Kotlin (squelette)

### 1. Déclaration du service (AndroidManifest.xml)

```xml
<service
    android:name=".LabosurfVpnService"
    android:permission="android.permission.BIND_VPN_SERVICE"
    android:exported="false">
    <intent-filter>
        <action android:name="android.net.VpnService" />
    </intent-filter>
</service>
```

### 2. Service VPN minimal

```kotlin
class LabosurfVpnService : VpnService() {

    private var vpnInterface: ParcelFileDescriptor? = null
    private var udpSocket: DatagramSocket? = null
    private var running = false

    companion object {
        const val ACTION_CONNECT = "com.labosurf.CONNECT"
        const val ACTION_DISCONNECT = "com.labosurf.DISCONNECT"
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_CONNECT -> connect(
                server = intent.getStringExtra("server")!!,
                port = intent.getIntExtra("port", 5667),
                username = intent.getStringExtra("username")!!,
                password = intent.getStringExtra("password")!!
            )
            ACTION_DISCONNECT -> disconnect()
        }
        return START_STICKY
    }

    private fun connect(server: String, port: Int, username: String, password: String) {
        // 1. Créer le socket UDP vers le VPS
        udpSocket = DatagramSocket()
        val serverAddr = InetSocketAddress(server, port)
        udpSocket.connect(serverAddr)

        // 2. Handshake LABOSURF (HELLO → CHALLENGE → AUTH → AUTH_OK)
        val tunnelIP = handshake(udpSocket, username, password)
            ?: run { stopSelf(); return }

        // 3. Créer l'interface VPN
        val builder = Builder()
            .setSession("LABOSURF")
            .addAddress(tunnelIP, 24)
            .addRoute("0.0.0.0", 0)   // tout le trafic dans le VPN
            .addDnsServer("8.8.8.8")
            .setMtu(1380)              // MTU réduit pour le tunnel UDP
            .setBlocking(true)

        vpnInterface = builder.establish()
        running = true

        // 4. Démarrer les boucles de transfert
        thread { tunToUdp() }    // TUN → serveur
        thread { udpToTun() }    // serveur → TUN
        thread { keepalive() }   // PING périodique
    }

    private fun handshake(socket: DatagramSocket, user: String, pass: String): String? {
        // HELLO
        socket.send(DatagramPacket("HELLO".toByteArray(), 5))

        // CHALLENGE
        val buf = ByteArray(1024)
        val pkt = DatagramPacket(buf, buf.size)
        socket.soTimeout = 5000
        socket.receive(pkt)
        val challenge = String(pkt.data, 0, pkt.length)
            .removePrefix("CHALLENGE ")

        // AUTH — HMAC-SHA256(nonce_bytes, password)
        val nonceBytes = challenge.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
        val mac = Mac.getInstance("HmacSHA256")
        mac.init(SecretKeySpec(pass.toByteArray(), "HmacSHA256"))
        val hmac = mac.doFinal(nonceBytes)
        val hmacHex = hmac.joinToString("") { "%02x".format(it) }

        socket.send(DatagramPacket("AUTH $hmacHex".toByteArray(), "AUTH $hmacHex".length))

        // AUTH_OK <ip>
        socket.receive(pkt)
        val response = String(pkt.data, 0, pkt.length)
        if (!response.startsWith("AUTH_OK ")) return null
        return response.removePrefix("AUTH_OK ").trim()
    }

    private fun tunToUdp() {
        val fd = vpnInterface?.fileDescriptor ?: return
        val input = FileInputStream(fd)
        val buffer = ByteArray(65535)

        while (running) {
            val n = input.read(buffer)
            if (n > 0) {
                val tunnelPkt = encodeTunnelPacket(clientID, buffer.copyOf(n))
                udpSocket?.send(DatagramPacket(tunnelPkt, tunnelPkt.size))
            }
        }
    }

    private fun udpToTun() {
        val fd = vpnInterface?.fileDescriptor ?: return
        val output = FileOutputStream(fd)
        val buffer = ByteArray(65535)

        while (running) {
            val pkt = DatagramPacket(buffer, buffer.size)
            try { udpSocket?.receive(pkt) } catch (_: Exception) { continue }
            val decoded = decodeTunnelPacket(pkt.data, pkt.length) ?: continue
            output.write(decoded.payload)
        }
    }

    private fun keepalive() {
        while (running) {
            Thread.sleep(25_000)
            try { udpSocket?.send(DatagramPacket("PING".toByteArray(), 4)) }
            catch (_: Exception) { }
        }
    }

    private fun disconnect() {
        running = false
        vpnInterface?.close()
        udpSocket?.close()
        stopSelf()
    }

    override fun onDestroy() { disconnect() }
    override fun onBind(intent: Intent?) = super.onBind(intent)
}
```

### 3. Encodage/décodage du protocole tunnel

```kotlin
private val clientID: Long by lazy {
    // SHA-256("local_ip:local_port")[0:8] en big-endian
    val addr = udpSocket.localAddress.hostAddress + ":" + udpSocket.localPort
    val digest = MessageDigest.getInstance("SHA-256").digest(addr.toByteArray())
    ByteBuffer.wrap(digest, 0, 8).long
}

fun encodeTunnelPacket(clientID: Long, payload: ByteArray): ByteArray {
    val buf = ByteBuffer.allocate(12 + payload.size)
    buf.put(1)           // version
    buf.put(byteArrayOf(0, 0, 0))  // réservé
    buf.putLong(clientID)
    buf.put(payload)
    return buf.array()
}

data class TunnelPacket(val clientID: Long, val payload: ByteArray)

fun decodeTunnelPacket(data: ByteArray, length: Int): TunnelPacket? {
    if (length < 12) return null
    if (data[0] != 1.toByte()) return null
    val buf = ByteBuffer.wrap(data, 0, length)
    buf.position(4)
    val id = buf.long
    val payload = ByteArray(length - 12)
    buf.get(payload)
    return TunnelPacket(id, payload)
}
```

## Permissions requises

```xml
<uses-permission android:name="android.permission.INTERNET" />
<uses-permission android:name="android.permission.FOREGROUND_SERVICE" />
```

L'utilisateur doit **accepter le dialogue VPN** la première fois :
```kotlin
val intent = VpnService.prepare(context)
if (intent != null) startActivityForResult(intent, REQUEST_VPN)
else startService(vpnIntent)  // déjà autorisé
```

## Notes importantes

- **MTU** : 1380 recommandé (1500 - 20 IP - 8 UDP - 12 tunnel - marge).
- **Routes** : `addRoute("0.0.0.0", 0)` capture tout le trafic. Pour un
  split-tunnel, ajouter des routes sélectives.
- **DNS** : toujours ajouter un serveur DNS dans le Builder.
- **Batterie** : le keepalive toutes les 25 s consomme peu mais doit
  utiliser `setUnderlyingNetworks()` pour optimiser.
- **Android 14+** : le service doit être déclaré avec
  `android:foregroundServiceType="specialUse"` ou similaire.

## Compilation

Un projet Android complet nécessite :
- Android Studio ou `gradlew` en ligne de commande
- SDK Android 24+ (Android 7.0)
- Aucune bibliothèque externe (le protocole tient en ~100 lignes de Kotlin)

Le projet Android n'est pas inclus dans ce dépôt — ce document est le
guide de référence pour le construire.
