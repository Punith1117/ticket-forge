package email

import (
	"fmt"
	"strings"
)

// RegistrationVerificationEmail constructs an email.Message for account registration verification.
func RegistrationVerificationEmail(from, recipient, verificationURL string) Message {
	subject := "Verify your email address for TicketForge"

	textBody := fmt.Sprintf(`Welcome to TicketForge!

Please verify your email address to complete your account registration by opening this link in your browser:
%s

This link will expire in 24 hours.

If you did not request this registration, you can safely ignore this email.

Best regards,
The TicketForge Team
`, verificationURL)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Verify your email address</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; line-height: 1.6; color: #1f2937; margin: 0; padding: 24px; background-color: #f9fafb;">
  <div style="max-width: 560px; margin: 0 auto; background: #ffffff; padding: 32px; border-radius: 8px; border: 1px solid #e5e7eb;">
    <h2 style="color: #111827; margin-top: 0; font-size: 20px;">Welcome to TicketForge</h2>
    <p>Thank you for initiating your account registration. Please verify your email address by clicking the button below:</p>
    <div style="text-align: center; margin: 28px 0;">
      <a href="%s" style="background-color: #2563eb; color: #ffffff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500; display: inline-block;">Verify Email Address</a>
    </div>
    <p style="font-size: 14px; color: #4b5563;">Or copy and paste this link into your browser:<br>
      <a href="%s" style="color: #2563eb; word-break: break-all;">%s</a>
    </p>
    <hr style="border: none; border-top: 1px solid #e5e7eb; margin: 24px 0;">
    <p style="font-size: 12px; color: #6b7280; margin-bottom: 0;">This verification link will expire in 24 hours. If you did not create a TicketForge account, please disregard this email.</p>
  </div>
</body>
</html>
`, verificationURL, verificationURL, verificationURL)

	return Message{
		From:     from,
		To:       []string{strings.TrimSpace(recipient)},
		Subject:  subject,
		TextBody: textBody,
		HTMLBody: htmlBody,
	}
}

// PasswordResetEmail constructs an email.Message for password reset requests.
func PasswordResetEmail(from, recipient, resetURL string) Message {
	subject := "Reset your TicketForge password"

	textBody := fmt.Sprintf(`Hello,

We received a request to reset your TicketForge password. You can reset your password by opening this link in your browser:
%s

This link will expire in 20 minutes.

If you did not request a password reset, you can safely ignore this email. Your password will remain unchanged.

Best regards,
The TicketForge Team
`, resetURL)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Reset your TicketForge password</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; line-height: 1.6; color: #1f2937; margin: 0; padding: 24px; background-color: #f9fafb;">
  <div style="max-width: 560px; margin: 0 auto; background: #ffffff; padding: 32px; border-radius: 8px; border: 1px solid #e5e7eb;">
    <h2 style="color: #111827; margin-top: 0; font-size: 20px;">Password Reset Request</h2>
    <p>We received a request to reset your password for TicketForge. Click the button below to choose a new password:</p>
    <div style="text-align: center; margin: 28px 0;">
      <a href="%s" style="background-color: #2563eb; color: #ffffff; text-decoration: none; padding: 12px 24px; border-radius: 6px; font-weight: 500; display: inline-block;">Reset Password</a>
    </div>
    <p style="font-size: 14px; color: #4b5563;">Or copy and paste this link into your browser:<br>
      <a href="%s" style="color: #2563eb; word-break: break-all;">%s</a>
    </p>
    <hr style="border: none; border-top: 1px solid #e5e7eb; margin: 24px 0;">
    <p style="font-size: 12px; color: #6b7280; margin-bottom: 0;">This password reset link will expire in 20 minutes. If you did not request this, no action is needed.</p>
  </div>
</body>
</html>
`, resetURL, resetURL, resetURL)

	return Message{
		From:     from,
		To:       []string{strings.TrimSpace(recipient)},
		Subject:  subject,
		TextBody: textBody,
		HTMLBody: htmlBody,
	}
}
