<?php
defined( 'ABSPATH' ) || exit;

class Waffle_Gateway extends WC_Payment_Gateway {
	public function __construct() {
		$this->id                 = 'waffle';
		$this->method_title       = 'Waffle';
		$this->method_description = __( 'Terima QRIS dan virtual account lewat halaman bayar Waffle.', 'waffle-woocommerce' );
		$this->has_fields         = false;
		$this->supports           = array( 'products' );
		$this->init_form_fields();
		$this->init_settings();
		$this->title       = $this->get_option( 'title' );
		$this->description = $this->get_option( 'description' );
		add_action( 'woocommerce_update_options_payment_gateways_' . $this->id, array( $this, 'process_admin_options' ) );
	}

	public function init_form_fields() {
		$secret = __( 'Dari dashboard Waffle, menu Developer.', 'waffle-woocommerce' );
		/* translators: %s: webhook URL */
		$hook              = sprintf( __( 'Daftarkan URL ini sebagai webhook di dashboard Waffle: %s', 'waffle-woocommerce' ), '<code>' . esc_html( rest_url( 'waffle/v1/webhook' ) ) . '</code>' );
		$this->form_fields = array(
			'enabled'        => array( 'title' => __( 'Aktif', 'waffle-woocommerce' ), 'type' => 'checkbox', 'label' => __( 'Aktifkan Waffle', 'waffle-woocommerce' ), 'default' => 'no' ),
			'title'          => array( 'title' => __( 'Judul', 'waffle-woocommerce' ), 'type' => 'text', 'default' => 'QRIS & Virtual Account' ),
			'description'    => array( 'title' => __( 'Deskripsi', 'waffle-woocommerce' ), 'type' => 'textarea', 'default' => __( 'Bayar dengan QRIS atau transfer virtual account.', 'waffle-woocommerce' ) ),
			'sandbox'        => array( 'title' => __( 'Mode uji coba', 'waffle-woocommerce' ), 'type' => 'checkbox', 'label' => __( 'Pakai sandbox, tanpa uang sungguhan', 'waffle-woocommerce' ), 'default' => 'yes', 'description' => $hook ),
			'sandbox_key'    => array( 'title' => __( 'API key sandbox', 'waffle-woocommerce' ), 'type' => 'password', 'description' => $secret ),
			'sandbox_secret' => array( 'title' => __( 'Webhook secret sandbox', 'waffle-woocommerce' ), 'type' => 'password' ),
			'live_key'       => array( 'title' => __( 'API key live', 'waffle-woocommerce' ), 'type' => 'password', 'description' => __( 'Tersedia setelah data usaha lolos verifikasi.', 'waffle-woocommerce' ) ),
			'live_secret'    => array( 'title' => __( 'Webhook secret live', 'waffle-woocommerce' ), 'type' => 'password' ),
			'expiry'         => array( 'title' => __( 'Batas bayar (menit)', 'waffle-woocommerce' ), 'type' => 'number', 'default' => '60', 'custom_attributes' => array( 'min' => 5, 'max' => 10080 ), 'description' => __( 'Samakan dengan batas tahan stok WooCommerce.', 'waffle-woocommerce' ) ),
		);
	}

	public function is_available() {
		return parent::is_available() && 'IDR' === get_woocommerce_currency() && '' !== Waffle_API::key() && '' !== Waffle_API::secret();
	}

	public function process_payment( $order_id ) {
		$order = wc_get_order( $order_id );
		$url   = Waffle_Orders::start( $order );
		if ( is_wp_error( $url ) ) {
			wc_get_logger()->error( 'order ' . $order_id . ': ' . $url->get_error_message(), array( 'source' => 'waffle' ) );
			wc_add_notice( __( 'Pembayaran belum bisa dibuka. Coba lagi sebentar atau hubungi penjual.', 'waffle-woocommerce' ), 'error' );
			return array( 'result' => 'failure' );
		}
		return array( 'result' => 'success', 'redirect' => $url );
	}
}
