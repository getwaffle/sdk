<?php
defined( 'ABSPATH' ) || exit;

/** Ties WooCommerce orders to Waffle charges. The API's own answer is the only proof of payment. */
class Waffle_Orders {
	/** Whole rupiah, or null: a fractional or non-IDR total is refused, never rounded. */
	public static function amount( WC_Order $order ) {
		$total = (float) $order->get_total();
		$int   = (int) round( $total );
		return 'IDR' === $order->get_currency() && $int > 0 && abs( $total - $int ) < 0.001 ? $int : null;
	}

	/** Random per install, so two shops on one Waffle account never share an idempotency key. */
	private static function site_id() {
		$id = get_option( 'waffle_wc_site_id' );
		if ( ! $id ) {
			add_option( 'waffle_wc_site_id', wp_generate_uuid4(), '', false );
			$id = get_option( 'waffle_wc_site_id' );
		}
		return $id;
	}

	/** Returns the hosted page URL, the order-received URL when already paid, or a WP_Error. */
	public static function start( WC_Order $order ) {
		return self::locked(
			$order->get_id(),
			static function () use ( $order ) {
				return self::start_locked( wc_get_order( $order->get_id() ) );
			}
		);
	}

	private static function start_locked( WC_Order $order ) {
		$amount = self::amount( $order );
		if ( null === $amount ) {
			return new WP_Error( 'waffle_amount', 'Total pesanan harus rupiah bulat.' );
		}
		$mode    = Waffle_API::mode();
		$attempt = (int) $order->get_meta( '_waffle_attempt' );
		$charge  = $order->get_meta( '_waffle_charge_id' );

		if ( $charge && $order->get_meta( '_waffle_mode' ) === $mode ) {
			$current = Waffle_API::get_charge( $charge );
			if ( is_wp_error( $current ) ) {
				return $current; // Unknown state: never risk a second charge.
			}
			if ( 'paid' === $current['status'] ) {
				self::apply( $order, $current );
				return $order->get_checkout_order_received_url();
			}
			if ( 'pending' === $current['status'] && (int) $order->get_meta( '_waffle_amount' ) === $amount ) {
				return Waffle_API::pay_url( $charge );
			}
			++$attempt;
		} elseif ( $charge ) {
			++$attempt;
		}

		// Saved before the call: a retry after a timeout reuses the same key.
		$order->update_meta_data( '_waffle_attempt', $attempt );
		$order->update_meta_data( '_waffle_mode', $mode );
		$order->update_meta_data( '_waffle_amount', $amount );
		$order->delete_meta_data( '_waffle_charge_id' );
		$order->save();

		$settings = Waffle_API::settings();
		$minutes  = isset( $settings['expiry'] ) ? (int) $settings['expiry'] : 60;
		$result   = Waffle_API::create_charge(
			array(
				'amount'                     => $amount,
				'currency'                   => 'IDR',
				/* translators: %s: order number */
				'description'                => sprintf( __( 'Pesanan #%s', 'waffle-woocommerce' ), $order->get_order_number() ),
				'checkout_channel_selection' => 'payer',
				'expires_in_minutes'         => min( 10080, max( 5, $minutes ) ),
				'return_url'                 => $order->get_checkout_order_received_url(),
				'metadata'                   => array(
					'wc_site'      => self::site_id(),
					'wc_order_id'  => (string) $order->get_id(),
					'wc_order_key' => $order->get_order_key(),
				),
			),
			'wc-' . self::site_id() . '-' . $order->get_id() . '-' . $mode . '-' . $amount . '-' . $attempt
		);
		if ( is_wp_error( $result ) ) {
			return $result;
		}
		if ( empty( $result['id'] ) || ! is_string( $result['id'] ) || ! isset( $result['mode'] ) || $result['mode'] !== $mode ) {
			return new WP_Error( 'waffle_response', 'Jawaban Waffle tidak sesuai.' );
		}
		$order->update_meta_data( '_waffle_charge_id', $result['id'] );
		$order->save();

		if ( function_exists( 'as_schedule_single_action' ) ) {
			as_schedule_single_action( time() + ( $minutes + 2 ) * MINUTE_IN_SECONDS, 'waffle_wc_check_order', array( $order->get_id() ), 'waffle' );
		}
		return Waffle_API::pay_url( $result['id'] );
	}

	/** Safety net for a missed webhook: thank-you page and one scheduled check. */
	public static function check( $order_id ) {
		$order = wc_get_order( $order_id );
		if ( ! $order || 'waffle' !== $order->get_payment_method() || $order->is_paid() || ! $order->get_meta( '_waffle_charge_id' ) ) {
			return;
		}
		$charge = Waffle_API::get_charge( $order->get_meta( '_waffle_charge_id' ) );
		if ( ! is_wp_error( $charge ) && 'paid' === $charge['status'] ) {
			self::sync( $charge );
		}
	}

	/** Applies a charge fetched from the API to its order. Returns false only when worth retrying. */
	public static function sync( array $charge ) {
		$meta  = isset( $charge['metadata'] ) && is_array( $charge['metadata'] ) ? $charge['metadata'] : array();
		$order = isset( $meta['wc_order_id'] ) ? wc_get_order( (int) $meta['wc_order_id'] ) : null;
		if ( ! $order instanceof WC_Order || empty( $meta['wc_site'] ) || self::site_id() !== $meta['wc_site']
			|| empty( $meta['wc_order_key'] ) || ! hash_equals( $order->get_order_key(), (string) $meta['wc_order_key'] ) ) {
			return true; // Not ours: another shop or a dashboard payment on the same account.
		}
		$result = self::locked(
			$order->get_id(),
			static function () use ( $order, $charge ) {
				self::apply( wc_get_order( $order->get_id() ), $charge );
				return true;
			}
		);
		return ! is_wp_error( $result );
	}

	private static function apply( WC_Order $order, array $charge ) {
		if ( 'paid' !== $charge['status'] || ! isset( $charge['mode'] ) || Waffle_API::mode() !== $charge['mode'] ) {
			return;
		}
		$id = $charge['id'];
		if ( $order->get_transaction_id() === $id || in_array( $id, (array) $order->get_meta( '_waffle_seen' ), true ) ) {
			return;
		}
		// What the shop asked for, before a fee the payer may carry.
		$base  = isset( $charge['breakdown']['base_amount'] ) ? $charge['breakdown']['base_amount'] : null;
		$valid = 'IDR' === $charge['currency'] && is_int( $base ) && self::amount( $order ) === $base;

		if ( $order->is_paid() || $order->has_status( array( 'refunded', 'cancelled' ) ) || ! $valid ) {
			$seen   = array_filter( (array) $order->get_meta( '_waffle_seen' ) );
			$seen[] = $id;
			$order->update_meta_data( '_waffle_seen', $seen );
			$note = sprintf( /* translators: %s: charge id */ __( 'Waffle: pembayaran %s diterima tapi perlu dicek manual (pesanan sudah lunas/dibatalkan, atau nominal berbeda).', 'waffle-woocommerce' ), $id );
			if ( $order->is_paid() || $order->has_status( 'refunded' ) ) {
				$order->add_order_note( $note );
				$order->save();
			} else {
				$order->update_status( 'on-hold', $note );
			}
			return;
		}
		$order->add_order_note( sprintf( /* translators: 1: channel 2: charge id */ __( 'Waffle: lunas lewat %1$s (%2$s).', 'waffle-woocommerce' ), isset( $charge['channel'] ) ? $charge['channel'] : '-', $id ) );
		$order->payment_complete( $id );
	}

	private static function locked( $order_id, callable $fn ) {
		global $wpdb;
		$name = 'waffle_' . md5( $wpdb->prefix . $order_id );
		// phpcs:ignore WordPress.DB.DirectDatabaseQuery
		if ( '1' !== (string) $wpdb->get_var( $wpdb->prepare( 'SELECT GET_LOCK(%s, 8)', $name ) ) ) {
			return new WP_Error( 'waffle_busy', 'Pesanan sedang diproses, coba lagi.' );
		}
		try {
			return $fn();
		} finally {
			// phpcs:ignore WordPress.DB.DirectDatabaseQuery
			$wpdb->query( $wpdb->prepare( 'SELECT RELEASE_LOCK(%s)', $name ) );
		}
	}
}
