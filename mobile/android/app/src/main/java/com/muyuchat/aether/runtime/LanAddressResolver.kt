package com.muyuchat.aether.runtime

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import java.net.Inet4Address
import java.net.InetAddress

/**
 * 本机局域网 IPv4 探测（只读，不做任何网络请求）。
 *
 * **为什么 Go 侧不自己枚举**：Android 的 SELinux 把 app 进程放在 `untrusted_app`
 * 域，普通应用拿不到 `NETLINK_ROUTE`；Go 的 `net.Interfaces()` 走 netlink，
 * 会直接失败（设备上 0.0.0.0 绑定成功、`lanUrls` 却为空就是这个原因）。
 * Java/ConnectivityManager 走 binder 到 system_server，这条路是通的，所以由
 * Kotlin 探测后经 `RuntimeConfig.lanAddressHint` 交给 Go，不新开 JNI 通道。
 *
 * 只返回「能上网」的网络（Wi‑Fi/以太网/蜂窝/VPN）上的、真实可路由的 IPv4：
 * 掉回环、掉 169.254 链路本地、掉 IPv6。
 */
internal object LanAddressResolver {
    fun ipv4Addresses(context: Context): List<String> {
        val manager = context.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager
            ?: return emptyList()
        val networks = runCatching { manager.allNetworks }.getOrElse { return emptyList() }
        val out = LinkedHashSet<String>()
        for (network in networks) {
            val capabilities = runCatching { manager.getNetworkCapabilities(network) }.getOrNull()
                ?: continue
            if (!capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) {
                continue
            }
            val linkProperties = runCatching { manager.getLinkProperties(network) }.getOrNull()
                ?: continue
            for (link in linkProperties.linkAddresses) {
                val address: InetAddress = link.address
                if (address !is Inet4Address) continue
                if (address.isLoopbackAddress || address.isLinkLocalAddress) continue
                val host = address.hostAddress ?: continue
                if (isRouteableLanIpv4(host)) out += host
            }
        }
        return out.toList()
    }
}

/** 一个 IPv4 字面量是否算"局域网里别的设备能填的地址"。 */
internal fun isRouteableLanIpv4(raw: String): Boolean {
    val text = raw.trim()
    // 只接受 a.b.c.d 四段十进制；其它（域名、IPv6、带掩码）一律不认。
    val parts = text.split('.')
    if (parts.size != 4) return false
    val numbers = parts.map { it.toIntOrNull() ?: return false }
    if (numbers.any { it !in 0..255 }) return false
    // 回环 / 非指定（0.0.0.0）/ 链路本地 169.254.x.x 都不算。
    if (numbers[0] == 127) return false
    if (numbers[0] == 0 && numbers[1] == 0 && numbers[2] == 0 && numbers[3] == 0) return false
    if (numbers[0] == 169 && numbers[1] == 254) return false
    return true
}

/**
 * 把探测结果折成给 Go 的 hint：只保留合法地址、去重、逗号分隔。
 * 空列表 → 空串（Go 会退回自己的枚举，仍拿不到就显示"未获取到局域网地址"）。
 */
internal fun lanAddressHint(addresses: List<String>): String =
    addresses.map { it.trim() }
        .filter { isRouteableLanIpv4(it) }
        .distinct()
        .joinToString(",")
