=== Waffle for WooCommerce ===
Contributors: waffle
Tags: qris, virtual account, indonesia, payment
Requires at least: 6.4
Tested up to: 7.1
Requires PHP: 7.4
Stable tag: 0.1.0
License: GPLv2 or later
License URI: https://www.gnu.org/licenses/gpl-2.0.html

Terima QRIS dan virtual account di WooCommerce lewat halaman bayar Waffle.

== Description ==

Pembeli memilih QRIS atau virtual account di halaman bayar Waffle. Pesanan ditandai lunas setelah pembayaran dikonfirmasi ke API Waffle.

Plugin ini memakai layanan luar: API Waffle (api.getwaffle.id) dan halaman bayar (getwaffle.id). Yang dikirim: nominal, nomor pesanan, ID dan order key pesanan, dan URL kembali. Syarat: https://getwaffle.id/legal/terms, privasi: https://getwaffle.id/legal/privacy.

Pembayaran live aktif setelah data usaha lolos verifikasi. Hanya IDR. Refund dan langganan belum didukung.

== Installation ==

1. Pasang dan aktifkan plugin.
2. Di WooCommerce, Pengaturan, Pembayaran, Waffle: isi API key.
3. Daftarkan URL webhook yang tampil di sana pada dashboard Waffle, lalu isi webhook secret.
4. Uji dengan mode uji coba sebelum memakai live.
