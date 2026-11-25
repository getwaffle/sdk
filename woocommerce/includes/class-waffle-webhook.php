<?php
defined( 'ABSPATH' ) || exit;

/** POST /wp-json/waffle/v1/webhook. Signed event in, then the charge is read back from the API. */
class Waffle_Webhook {
	public static function register() {
		register_rest_route(
			'waffle/v1',
			'/webhook',
			array(
				'methods'             => 'POST',
				'callback'            => array( __CLASS__, 'handle' ),
				'permission_callback' => '__return_true', // Authenticated by the signature below.
			)
		);
	}

	public static function verify( $body, $header, $secret, $now ) {
		if ( '' === $secret || ! preg_match( '/^t=(\d{1,12}),v1=([0-9a-f]{64})$/', (string) $header, $m ) || abs( $now - (int) $m[1] ) > 300 ) {
			return false;
		}
		return hash_equals( hash_hmac( 'sha256', $m[1] . '.' . $body, $secret ), $m[2] );
	}

	public static function handle( WP_REST_Request $request ) {
		$body = $request->get_body();
		if ( ! self::verify( $body, $request->get_header( 'X-Waffle-Signature-V2' ), Waffle_API::secret(), time() ) ) {
			return new WP_REST_Response( array( 'error' => 'invalid signature' ), 401 );
		}
		$event = json_decode( $body, true );
		if ( ! is_array( $event ) || ! isset( $event['event'] ) || 'charge.paid' !== $event['event'] || empty( $event['charge_id'] ) || ! is_string( $event['charge_id'] ) ) {
			return new WP_REST_Response( array( 'ok' => true ), 200 );
		}
		$charge = Waffle_API::get_charge( $event['charge_id'] );
		if ( is_wp_error( $charge ) ) {
			$data = $charge->get_error_data();
			// 404: not a charge of this key. Anything else is retried by Waffle.
			return new WP_REST_Response( array( 'error' => 'charge lookup failed' ), isset( $data['status'] ) && 404 === $data['status'] ? 200 : 503 );
		}
		return Waffle_Orders::sync( $charge )
			? new WP_REST_Response( array( 'ok' => true ), 200 )
			: new WP_REST_Response( array( 'error' => 'busy' ), 503 );
	}
}
